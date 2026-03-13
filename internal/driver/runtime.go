package driver

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sort"

	"ex-otogi/pkg/otogi/core"
	"ex-otogi/pkg/otogi/platform"
)

// Definition describes one configured driver entry.
type Definition struct {
	// Name is the stable configured driver instance identifier.
	Name string
	// Type identifies which builder should construct this runtime.
	Type string
	// Enabled controls whether this definition is active.
	Enabled bool
	// Config stores driver-type-specific JSON payload.
	Config []byte
}

// Runtime contains one fully built driver runtime instance.
//
// All adapters in one runtime should describe the same configured driver
// instance and Source identity so shared runtime policies can correlate inbound
// and outbound traffic consistently.
type Runtime struct {
	// Source identifies the concrete event source produced by Driver.
	Source platform.EventSource
	// Driver is the inbound runtime implementation registered with kernel.
	Driver core.Driver
	// MediaDownloader adapts media download operations for this runtime when supported.
	MediaDownloader platform.MediaDownloader
	// SinkDispatcher adapts outbound operations for this runtime when supported.
	SinkDispatcher platform.SinkDispatcher
	// ModerationDispatcher adapts moderation operations for this runtime when supported.
	ModerationDispatcher platform.ModerationDispatcher
}

// BuilderFunc builds one runtime from one configured driver definition.
//
// Builders should keep platform SDK types internal and return only adapters
// that satisfy Otogi contracts.
type BuilderFunc func(ctx context.Context, definition Definition, logger *slog.Logger) (Runtime, error)

// Descriptor binds one driver type token to platform metadata and a runtime builder.
type Descriptor struct {
	// Type is the driver type token from configuration (for example "telegram").
	Type string
	// Platform is the Otogi platform for this driver type.
	Platform platform.Platform
	// Builder constructs one runtime instance for this driver type.
	Builder BuilderFunc
}

type registryEntry struct {
	platform platform.Platform
	builder  BuilderFunc
}

// Registry maps driver types to runtime builders and type-level platform metadata.
type Registry struct {
	entries map[string]registryEntry
	types   []string
}

// NewRegistry creates one immutable driver registry from descriptors.
func NewRegistry(descriptors []Descriptor) (*Registry, error) {
	entries := make(map[string]registryEntry, len(descriptors))
	types := make([]string, 0, len(descriptors))
	for _, descriptor := range descriptors {
		if descriptor.Type == "" {
			return nil, fmt.Errorf("new registry: empty descriptor type")
		}
		if descriptor.Platform == "" {
			return nil, fmt.Errorf("new registry type %s: empty platform", descriptor.Type)
		}
		if descriptor.Builder == nil {
			return nil, fmt.Errorf("new registry type %s: nil builder", descriptor.Type)
		}
		if _, exists := entries[descriptor.Type]; exists {
			return nil, fmt.Errorf("new registry type %s: duplicate", descriptor.Type)
		}

		entries[descriptor.Type] = registryEntry{
			platform: descriptor.Platform,
			builder:  descriptor.Builder,
		}
		types = append(types, descriptor.Type)
	}
	sort.Strings(types)

	return &Registry{
		entries: entries,
		types:   types,
	}, nil
}

// Types returns all registered driver types in deterministic sorted order.
func (r *Registry) Types() []string {
	if r == nil {
		return nil
	}

	types := make([]string, len(r.types))
	copy(types, r.types)

	return types
}

// PlatformForType resolves one registered driver type to its Otogi platform.
func (r *Registry) PlatformForType(driverType string) (platform.Platform, error) {
	if r == nil {
		return "", fmt.Errorf("resolve platform: nil registry")
	}

	entry, exists := r.entries[driverType]
	if !exists {
		return "", fmt.Errorf("unsupported type %s", driverType)
	}

	return entry.platform, nil
}

// BuildEnabled builds all enabled driver definitions.
//
// The returned runtimes are standardized by the registry: identity is
// validated, default source IDs are filled, and shared runtime decorators such
// as the article tag bridge are applied when supported by the runtime shape.
func (r *Registry) BuildEnabled(
	ctx context.Context,
	definitions []Definition,
	logger *slog.Logger,
) ([]Runtime, error) {
	if r == nil {
		return nil, fmt.Errorf("build drivers: nil registry")
	}

	runtimes := make([]Runtime, 0, len(definitions))
	seenNames := make(map[string]struct{}, len(definitions))
	for _, definition := range definitions {
		if !definition.Enabled {
			continue
		}
		if definition.Name == "" {
			return nil, fmt.Errorf("build driver: empty name")
		}
		if _, exists := seenNames[definition.Name]; exists {
			return nil, fmt.Errorf("build driver %s: duplicate name", definition.Name)
		}
		seenNames[definition.Name] = struct{}{}
		if definition.Type == "" {
			return nil, fmt.Errorf("build driver %s: empty type", definition.Name)
		}

		entry, exists := r.entries[definition.Type]
		if !exists {
			return nil, fmt.Errorf("build driver %s type %s: unsupported type", definition.Name, definition.Type)
		}

		runtime, err := entry.builder(ctx, definition, logger)
		if err != nil {
			return nil, fmt.Errorf("build driver %s type %s: %w", definition.Name, definition.Type, err)
		}
		if runtime.Driver == nil {
			return nil, fmt.Errorf("build driver %s type %s: nil driver", definition.Name, definition.Type)
		}
		if runtime.Source.Platform == "" {
			return nil, fmt.Errorf("build driver %s type %s: missing source platform", definition.Name, definition.Type)
		}
		if runtime.Source.ID == "" {
			runtime.Source.ID = definition.Name
		}

		runtimes = append(runtimes, runtime)
	}

	return wrapRuntimesWithArticleTags(runtimes), nil
}

type mediaRoute struct {
	ref        platform.EventSource
	downloader platform.MediaDownloader
}

type mediaDispatcher struct {
	byID           map[string]mediaRoute
	byPlatform     map[platform.Platform][]string
	sortedSourceID []string
}

// NewMediaDownloader creates a media downloader router from runtime instances.
func NewMediaDownloader(runtimes []Runtime) (platform.MediaDownloader, error) {
	byID := make(map[string]mediaRoute)
	byPlatform := make(map[platform.Platform][]string)
	sortedIDs := make([]string, 0, len(runtimes))
	for _, runtime := range runtimes {
		if runtime.MediaDownloader == nil {
			continue
		}
		if runtime.Source.ID == "" {
			return nil, fmt.Errorf("new media downloader: missing source id")
		}
		if _, exists := byID[runtime.Source.ID]; exists {
			return nil, fmt.Errorf("new media downloader: duplicate source id %s", runtime.Source.ID)
		}

		ref := runtime.Source
		byID[ref.ID] = mediaRoute{
			ref:        ref,
			downloader: runtime.MediaDownloader,
		}
		byPlatform[ref.Platform] = append(byPlatform[ref.Platform], ref.ID)
		sortedIDs = append(sortedIDs, ref.ID)
	}
	sort.Strings(sortedIDs)

	return &mediaDispatcher{
		byID:           byID,
		byPlatform:     byPlatform,
		sortedSourceID: sortedIDs,
	}, nil
}

// Download routes one media download request to one concrete driver downloader.
func (d *mediaDispatcher) Download(
	ctx context.Context,
	request platform.MediaDownloadRequest,
	output io.Writer,
) (platform.MediaAttachment, error) {
	if err := request.Validate(); err != nil {
		return platform.MediaAttachment{}, fmt.Errorf("validate media download request: %w", err)
	}
	if output == nil {
		return platform.MediaAttachment{}, fmt.Errorf("%w: missing output writer", platform.ErrInvalidMediaDownloadRequest)
	}

	downloader, err := d.resolve(request.Source)
	if err != nil {
		return platform.MediaAttachment{}, fmt.Errorf("resolve media downloader: %w", err)
	}

	attachment, err := downloader.Download(ctx, request, output)
	if err != nil {
		return platform.MediaAttachment{}, fmt.Errorf("route media download: %w", err)
	}

	return attachment, nil
}

func (d *mediaDispatcher) resolve(source platform.EventSource) (platform.MediaDownloader, error) {
	if d == nil {
		return nil, fmt.Errorf("nil dispatcher")
	}
	if len(d.byID) == 0 {
		return nil, fmt.Errorf("%w: no media downloaders configured", platform.ErrMediaDownloadUnsupported)
	}

	route, exists := d.byID[source.ID]
	if !exists {
		return nil, fmt.Errorf("%w: source %s not found", platform.ErrMediaDownloadUnsupported, source.ID)
	}
	if source.Platform != "" && route.ref.Platform != source.Platform {
		return nil, fmt.Errorf(
			"%w: source %s platform mismatch: expected %s got %s",
			platform.ErrMediaDownloadUnsupported,
			source.ID,
			source.Platform,
			route.ref.Platform,
		)
	}

	return route.downloader, nil
}

type sinkRoute struct {
	ref        platform.EventSink
	dispatcher platform.SinkDispatcher
}

type sinkDispatcher struct {
	byID         map[string]sinkRoute
	byPlatform   map[platform.Platform][]string
	sortedSinkID []string
}

// NewSinkDispatcher creates a sink dispatcher from runtime sinks.
func NewSinkDispatcher(runtimes []Runtime) (platform.SinkDispatcher, error) {
	byID := make(map[string]sinkRoute)
	byPlatform := make(map[platform.Platform][]string)
	sortedIDs := make([]string, 0, len(runtimes))
	for _, runtime := range runtimes {
		if runtime.SinkDispatcher == nil {
			continue
		}
		if runtime.Source.ID == "" {
			return nil, fmt.Errorf("new sink dispatcher: missing sink id")
		}
		if _, exists := byID[runtime.Source.ID]; exists {
			return nil, fmt.Errorf("new sink dispatcher: duplicate sink id %s", runtime.Source.ID)
		}

		ref := platform.EventSink{
			Platform: runtime.Source.Platform,
			ID:       runtime.Source.ID,
		}
		byID[ref.ID] = sinkRoute{
			ref:        ref,
			dispatcher: runtime.SinkDispatcher,
		}
		byPlatform[ref.Platform] = append(byPlatform[ref.Platform], ref.ID)
		sortedIDs = append(sortedIDs, ref.ID)
	}
	sort.Strings(sortedIDs)

	return &sinkDispatcher{
		byID:         byID,
		byPlatform:   byPlatform,
		sortedSinkID: sortedIDs,
	}, nil
}

// SendMessage routes send-message requests to one concrete sink.
func (d *sinkDispatcher) SendMessage(
	ctx context.Context,
	request platform.SendMessageRequest,
) (*platform.OutboundMessage, error) {
	dispatcher, err := d.resolve(request.Target)
	if err != nil {
		return nil, fmt.Errorf("resolve sink for send message: %w", err)
	}

	response, err := dispatcher.SendMessage(ctx, request)
	if err != nil {
		return nil, fmt.Errorf("route send message: %w", err)
	}

	return response, nil
}

// EditMessage routes edit-message requests to one concrete sink.
func (d *sinkDispatcher) EditMessage(ctx context.Context, request platform.EditMessageRequest) error {
	dispatcher, err := d.resolve(request.Target)
	if err != nil {
		return fmt.Errorf("resolve sink for edit message: %w", err)
	}

	if err := dispatcher.EditMessage(ctx, request); err != nil {
		return fmt.Errorf("route edit message: %w", err)
	}

	return nil
}

// DeleteMessage routes delete-message requests to one concrete sink.
func (d *sinkDispatcher) DeleteMessage(ctx context.Context, request platform.DeleteMessageRequest) error {
	dispatcher, err := d.resolve(request.Target)
	if err != nil {
		return fmt.Errorf("resolve sink for delete message: %w", err)
	}

	if err := dispatcher.DeleteMessage(ctx, request); err != nil {
		return fmt.Errorf("route delete message: %w", err)
	}

	return nil
}

// SetReaction routes set-reaction requests to one concrete sink.
func (d *sinkDispatcher) SetReaction(ctx context.Context, request platform.SetReactionRequest) error {
	dispatcher, err := d.resolve(request.Target)
	if err != nil {
		return fmt.Errorf("resolve sink for set reaction: %w", err)
	}

	if err := dispatcher.SetReaction(ctx, request); err != nil {
		return fmt.Errorf("route set reaction: %w", err)
	}

	return nil
}

// ListSinks returns all known concrete sinks.
func (d *sinkDispatcher) ListSinks(ctx context.Context) ([]platform.EventSink, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("list sinks: %w", err)
	}
	sinks := make([]platform.EventSink, 0, len(d.sortedSinkID))
	for _, id := range d.sortedSinkID {
		route, exists := d.byID[id]
		if !exists {
			continue
		}
		sinks = append(sinks, route.ref)
	}

	return sinks, nil
}

// ListSinksByPlatform returns all known concrete sinks for one platform.
func (d *sinkDispatcher) ListSinksByPlatform(
	ctx context.Context,
	targetPlatform platform.Platform,
) ([]platform.EventSink, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("list sinks by platform: %w", err)
	}

	ids := d.byPlatform[targetPlatform]
	sinks := make([]platform.EventSink, 0, len(ids))
	for _, id := range ids {
		route, exists := d.byID[id]
		if !exists {
			continue
		}
		sinks = append(sinks, route.ref)
	}

	return sinks, nil
}

func (d *sinkDispatcher) resolve(target platform.OutboundTarget) (platform.SinkDispatcher, error) {
	if d == nil {
		return nil, fmt.Errorf("nil dispatcher")
	}
	if len(d.byID) == 0 {
		return nil, fmt.Errorf("%w: no sinks configured", platform.ErrOutboundUnsupported)
	}

	if target.Sink != nil {
		return d.resolveSinkRef(*target.Sink)
	}
	if len(d.byID) == 1 {
		for _, route := range d.byID {
			return route.dispatcher, nil
		}
	}

	return nil, fmt.Errorf("%w: missing target sink", platform.ErrOutboundUnsupported)
}

func (d *sinkDispatcher) resolveSinkRef(ref platform.EventSink) (platform.SinkDispatcher, error) {
	if ref.ID != "" {
		route, exists := d.byID[ref.ID]
		if !exists {
			return nil, fmt.Errorf("%w: sink %s not found", platform.ErrOutboundUnsupported, ref.ID)
		}
		if ref.Platform != "" && route.ref.Platform != ref.Platform {
			return nil, fmt.Errorf(
				"%w: sink %s platform mismatch: expected %s got %s",
				platform.ErrOutboundUnsupported,
				ref.ID,
				ref.Platform,
				route.ref.Platform,
			)
		}

		return route.dispatcher, nil
	}
	if ref.Platform != "" {
		ids := d.byPlatform[ref.Platform]
		if len(ids) == 0 {
			return nil, fmt.Errorf("%w: no sink for platform %s", platform.ErrOutboundUnsupported, ref.Platform)
		}
		if len(ids) > 1 {
			return nil, fmt.Errorf("%w: ambiguous sink for platform %s", platform.ErrOutboundUnsupported, ref.Platform)
		}
		route, exists := d.byID[ids[0]]
		if !exists {
			return nil, fmt.Errorf("%w: sink %s not found", platform.ErrOutboundUnsupported, ids[0])
		}

		return route.dispatcher, nil
	}

	return nil, fmt.Errorf("%w: empty sink reference", platform.ErrOutboundUnsupported)
}

type moderationRoute struct {
	ref        platform.EventSink
	dispatcher platform.ModerationDispatcher
}

type moderationDispatcher struct {
	byID         map[string]moderationRoute
	byPlatform   map[platform.Platform][]string
	sortedSinkID []string
}

// NewModerationDispatcher creates a moderation dispatcher from runtime instances.
func NewModerationDispatcher(runtimes []Runtime) (platform.ModerationDispatcher, error) {
	byID := make(map[string]moderationRoute)
	byPlatform := make(map[platform.Platform][]string)
	sortedIDs := make([]string, 0, len(runtimes))
	for _, runtime := range runtimes {
		if runtime.ModerationDispatcher == nil {
			continue
		}
		if runtime.Source.ID == "" {
			return nil, fmt.Errorf("new moderation dispatcher: missing sink id")
		}
		if _, exists := byID[runtime.Source.ID]; exists {
			return nil, fmt.Errorf("new moderation dispatcher: duplicate sink id %s", runtime.Source.ID)
		}

		ref := platform.EventSink{
			Platform: runtime.Source.Platform,
			ID:       runtime.Source.ID,
		}
		byID[ref.ID] = moderationRoute{
			ref:        ref,
			dispatcher: runtime.ModerationDispatcher,
		}
		byPlatform[ref.Platform] = append(byPlatform[ref.Platform], ref.ID)
		sortedIDs = append(sortedIDs, ref.ID)
	}
	sort.Strings(sortedIDs)

	return &moderationDispatcher{
		byID:         byID,
		byPlatform:   byPlatform,
		sortedSinkID: sortedIDs,
	}, nil
}

// RestrictMember routes restrict-member requests to one concrete moderation dispatcher.
func (d *moderationDispatcher) RestrictMember(
	ctx context.Context,
	request platform.RestrictMemberRequest,
) error {
	dispatcher, err := d.resolve(request.Target)
	if err != nil {
		return fmt.Errorf("resolve sink for restrict member: %w", err)
	}

	if err := dispatcher.RestrictMember(ctx, request); err != nil {
		return fmt.Errorf("route restrict member: %w", err)
	}

	return nil
}

func (d *moderationDispatcher) resolve(target platform.OutboundTarget) (platform.ModerationDispatcher, error) {
	if d == nil {
		return nil, fmt.Errorf("nil moderation dispatcher")
	}
	if len(d.byID) == 0 {
		return nil, fmt.Errorf("%w: no moderation sinks configured", platform.ErrOutboundUnsupported)
	}

	if target.Sink != nil {
		return d.resolveSinkRef(*target.Sink)
	}
	if len(d.byID) == 1 {
		for _, route := range d.byID {
			return route.dispatcher, nil
		}
	}

	return nil, fmt.Errorf("%w: missing target sink for moderation", platform.ErrOutboundUnsupported)
}

func (d *moderationDispatcher) resolveSinkRef(ref platform.EventSink) (platform.ModerationDispatcher, error) {
	if ref.ID != "" {
		route, exists := d.byID[ref.ID]
		if !exists {
			return nil, fmt.Errorf("%w: moderation sink %s not found", platform.ErrOutboundUnsupported, ref.ID)
		}
		if ref.Platform != "" && route.ref.Platform != ref.Platform {
			return nil, fmt.Errorf(
				"%w: moderation sink %s platform mismatch: expected %s got %s",
				platform.ErrOutboundUnsupported,
				ref.ID,
				ref.Platform,
				route.ref.Platform,
			)
		}

		return route.dispatcher, nil
	}
	if ref.Platform != "" {
		ids := d.byPlatform[ref.Platform]
		if len(ids) == 0 {
			return nil, fmt.Errorf("%w: no moderation sink for platform %s", platform.ErrOutboundUnsupported, ref.Platform)
		}
		if len(ids) > 1 {
			return nil, fmt.Errorf("%w: ambiguous moderation sink for platform %s", platform.ErrOutboundUnsupported, ref.Platform)
		}
		route, exists := d.byID[ids[0]]
		if !exists {
			return nil, fmt.Errorf("%w: moderation sink %s not found", platform.ErrOutboundUnsupported, ids[0])
		}

		return route.dispatcher, nil
	}

	return nil, fmt.Errorf("%w: empty moderation sink reference", platform.ErrOutboundUnsupported)
}
