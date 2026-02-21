package driver

import (
	"context"
	"fmt"
	"log/slog"
	"sort"

	"ex-otogi/pkg/otogi"
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
type Runtime struct {
	// Source identifies the concrete event source produced by Driver.
	Source otogi.EventSource
	// Driver is the inbound runtime implementation registered with kernel.
	Driver otogi.Driver
	// SinkDispatcher adapts outbound operations for this runtime when supported.
	SinkDispatcher otogi.SinkDispatcher
}

// BuilderFunc builds one runtime from one configured driver definition.
type BuilderFunc func(ctx context.Context, definition Definition, logger *slog.Logger) (Runtime, error)

// Descriptor binds one driver type token to platform metadata and a runtime builder.
type Descriptor struct {
	// Type is the driver type token from configuration (for example "telegram").
	Type string
	// Platform is the neutral otogi platform for this driver type.
	Platform otogi.Platform
	// Builder constructs one runtime instance for this driver type.
	Builder BuilderFunc
}

type registryEntry struct {
	platform otogi.Platform
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

// PlatformForType resolves one registered driver type to its neutral otogi platform.
func (r *Registry) PlatformForType(driverType string) (otogi.Platform, error) {
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

	return runtimes, nil
}

type sinkRoute struct {
	ref        otogi.EventSink
	dispatcher otogi.SinkDispatcher
}

// CompositeSinkDispatcher routes sink operations to per-driver dispatchers.
type CompositeSinkDispatcher struct {
	byID         map[string]sinkRoute
	byPlatform   map[otogi.Platform][]string
	sortedSinkID []string
}

// NewCompositeSinkDispatcher creates a composite dispatcher from runtime sinks.
func NewCompositeSinkDispatcher(runtimes []Runtime) (*CompositeSinkDispatcher, error) {
	byID := make(map[string]sinkRoute)
	byPlatform := make(map[otogi.Platform][]string)
	sortedIDs := make([]string, 0, len(runtimes))
	for _, runtime := range runtimes {
		if runtime.SinkDispatcher == nil {
			continue
		}
		if runtime.Source.ID == "" {
			return nil, fmt.Errorf("new composite sink dispatcher: missing sink id")
		}
		if _, exists := byID[runtime.Source.ID]; exists {
			return nil, fmt.Errorf("new composite sink dispatcher: duplicate sink id %s", runtime.Source.ID)
		}

		ref := otogi.EventSink{
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

	return &CompositeSinkDispatcher{
		byID:         byID,
		byPlatform:   byPlatform,
		sortedSinkID: sortedIDs,
	}, nil
}

// SendMessage routes send-message requests to one concrete sink.
func (d *CompositeSinkDispatcher) SendMessage(
	ctx context.Context,
	request otogi.SendMessageRequest,
) (*otogi.OutboundMessage, error) {
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
func (d *CompositeSinkDispatcher) EditMessage(ctx context.Context, request otogi.EditMessageRequest) error {
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
func (d *CompositeSinkDispatcher) DeleteMessage(ctx context.Context, request otogi.DeleteMessageRequest) error {
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
func (d *CompositeSinkDispatcher) SetReaction(ctx context.Context, request otogi.SetReactionRequest) error {
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
func (d *CompositeSinkDispatcher) ListSinks(ctx context.Context) ([]otogi.EventSink, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("list sinks: %w", err)
	}
	sinks := make([]otogi.EventSink, 0, len(d.sortedSinkID))
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
func (d *CompositeSinkDispatcher) ListSinksByPlatform(
	ctx context.Context,
	platform otogi.Platform,
) ([]otogi.EventSink, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("list sinks by platform: %w", err)
	}

	ids := d.byPlatform[platform]
	sinks := make([]otogi.EventSink, 0, len(ids))
	for _, id := range ids {
		route, exists := d.byID[id]
		if !exists {
			continue
		}
		sinks = append(sinks, route.ref)
	}

	return sinks, nil
}

func (d *CompositeSinkDispatcher) resolve(target otogi.OutboundTarget) (otogi.SinkDispatcher, error) {
	if d == nil {
		return nil, fmt.Errorf("nil dispatcher")
	}
	if len(d.byID) == 0 {
		return nil, fmt.Errorf("%w: no sinks configured", otogi.ErrOutboundUnsupported)
	}

	if target.Sink != nil {
		return d.resolveSinkRef(*target.Sink)
	}
	if len(d.byID) == 1 {
		for _, route := range d.byID {
			return route.dispatcher, nil
		}
	}

	return nil, fmt.Errorf("%w: missing target sink", otogi.ErrOutboundUnsupported)
}

func (d *CompositeSinkDispatcher) resolveSinkRef(ref otogi.EventSink) (otogi.SinkDispatcher, error) {
	if ref.ID != "" {
		route, exists := d.byID[ref.ID]
		if !exists {
			return nil, fmt.Errorf("%w: sink %s not found", otogi.ErrOutboundUnsupported, ref.ID)
		}
		if ref.Platform != "" && route.ref.Platform != ref.Platform {
			return nil, fmt.Errorf(
				"%w: sink %s platform mismatch: expected %s got %s",
				otogi.ErrOutboundUnsupported,
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
			return nil, fmt.Errorf("%w: no sink for platform %s", otogi.ErrOutboundUnsupported, ref.Platform)
		}
		if len(ids) > 1 {
			return nil, fmt.Errorf("%w: ambiguous sink for platform %s", otogi.ErrOutboundUnsupported, ref.Platform)
		}
		route, exists := d.byID[ids[0]]
		if !exists {
			return nil, fmt.Errorf("%w: sink %s not found", otogi.ErrOutboundUnsupported, ids[0])
		}

		return route.dispatcher, nil
	}

	return nil, fmt.Errorf("%w: empty sink reference", otogi.ErrOutboundUnsupported)
}
