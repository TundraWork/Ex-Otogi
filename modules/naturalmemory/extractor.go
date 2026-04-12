package naturalmemory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"ex-otogi/pkg/otogi/ai"
	"ex-otogi/pkg/otogi/core"
	panel "ex-otogi/pkg/otogi/management"
	"ex-otogi/pkg/otogi/platform"
)

var validExtractionCategories = map[string]struct{}{
	"experience": {},
	"knowledge":  {},
	"preference": {},
	"reflection": {},
	"user_fact":  {},
}

// extractionAction identifies what the extraction LLM wants to do with a fact.
type extractionAction string

const (
	extractionActionNew    extractionAction = "new"
	extractionActionUpdate extractionAction = "update"
	extractionActionDelete extractionAction = "delete"
	extractionActionNoop   extractionAction = "noop"
)

type extractedMemory struct {
	Action           extractionAction `json:"action"`
	TargetID         string           `json:"target_id"`
	Content          string           `json:"content"`
	Category         string           `json:"category"`
	Importance       int              `json:"importance"`
	SubjectActorID   string           `json:"subject_actor_id"`
	SubjectActorName string           `json:"subject_actor_name"`
	ValidUntil       string           `json:"valid_until"`
	Keywords         []string         `json:"keywords"`
	Tags             []string         `json:"tags"`
}

type extractionContext struct {
	ConversationText string
	AnchorTime       time.Time
	SourceArticleID  string
	SourceActor      platform.Actor
	Participants     []ai.LLMMemoryActorRef
}

// processWindow processes one flushed window: resolves reply context, runs
// the extraction LLM, batches embeddings, and applies each extracted action.
func (m *Module) processWindow(
	ctx context.Context,
	scope ai.LLMMemoryScope,
	articles []bufferedArticle,
	reason FlushReason,
) error {
	if len(articles) == 0 {
		return nil
	}
	if m == nil || m.llmMemory == nil || m.extractionProvider == nil || m.embeddingProvider == nil {
		return fmt.Errorf("process window: required services unavailable")
	}
	if m.recorder != nil {
		ctx = panel.WithRecorder(ctx, m.recorder)
	}

	m.emitManagementEvent(ctx, "memory.window.flushed", "flushed article window for extraction", panel.TruncateDescription(fmt.Sprintf("%s (%d articles)", reason, len(articles))), panel.MemoryWindowFlushedPayload{
		Reason:       string(reason),
		ArticleCount: len(articles),
	})

	// 1. resolve reply quotes
	lookups := collectPendingReplyLookups(scope, articles)
	var quotes map[string]core.Memory
	if len(lookups) > 0 && m.memory != nil {
		resolved, err := loadReplyTargets(ctx, m.memory, lookups)
		if err != nil && m.logger != nil {
			m.logger.WarnContext(ctx, "naturalmemory resolve reply targets", "error", err)
		}
		quotes = resolved
	}

	// 2. serialize conversation
	convText, _ := serializeWindowConversation(articles, quotes, m.cfg.ExtractionMaxInputRunes)
	if strings.TrimSpace(convText) == "" {
		return nil
	}

	contextWindow := extractionContext{
		ConversationText: convText,
		AnchorTime:       latestOccurredAt(articles),
		Participants:     participantsFromArticles(articles, quotes),
	}

	// 3. embed window text + retrieve relevant existing memories
	windowEmbedding, err := embedSingleText(ctx, m.embeddingProvider, convText, ai.EmbeddingTaskTypeDocument)
	if err != nil {
		return fmt.Errorf("process window embed context: %w", err)
	}
	relevantExisting, err := m.llmMemory.Search(ctx, ai.LLMMemoryQuery{
		Scope:         scope,
		Embedding:     windowEmbedding,
		Limit:         m.cfg.RetrievalSearchLimit,
		MinSimilarity: 0,
	})
	if err != nil {
		return fmt.Errorf("process window search existing: %w", err)
	}

	m.emitManagementEvent(ctx, "memory.extract.started", "started natural memory extraction", "", panel.MemoryExtractStartedPayload{
		SourceKind:   "window",
		SegmentCount: len(articles),
	})

	// 4. extraction-with-action LLM (the only LLM call per flush)
	candidates, err := m.runExtractionLLM(ctx, contextWindow, matchRecords(relevantExisting))
	if err != nil {
		return fmt.Errorf("process window extraction: %w", err)
	}
	if len(candidates) == 0 {
		m.emitManagementEvent(ctx, "memory.extract.completed", "completed natural memory extraction", "", panel.MemoryExtractCompletedPayload{
			ExtractedCount: 0,
			Consolidated:   false,
		})
		return nil
	}

	// 5. batch-embed all NEW/UPDATE contents
	needEmbed := filterCandidatesForEmbed(candidates)
	embeddings, err := m.embedCandidatesBatch(ctx, needEmbed)
	if err != nil {
		return fmt.Errorf("process window embed candidates: %w", err)
	}

	// 6. apply each action
	embIdx := 0
	for _, c := range candidates {
		var applyErr error
		switch c.Action {
		case extractionActionNew:
			applyErr = m.applyNew(ctx, scope, contextWindow, c, embeddings[embIdx])
			embIdx++
		case extractionActionUpdate:
			applyErr = m.applyUpdate(ctx, scope, contextWindow, c, embeddings[embIdx], relevantExisting)
			embIdx++
		case extractionActionDelete:
			applyErr = m.applyDelete(ctx, c.TargetID, relevantExisting)
		case extractionActionNoop:
			continue
		default:
			continue
		}
		if applyErr != nil {
			m.debugCandidateError(ctx, c, applyErr)
		}
	}

	m.emitManagementEvent(ctx, "memory.extract.completed", "completed natural memory extraction", "", panel.MemoryExtractCompletedPayload{
		ExtractedCount: len(candidates),
		Consolidated:   false,
	})

	return nil
}

func latestOccurredAt(articles []bufferedArticle) time.Time {
	var latest time.Time
	for _, a := range articles {
		if a.OccurredAt.After(latest) {
			latest = a.OccurredAt
		}
	}
	if latest.IsZero() {
		return time.Now().UTC()
	}
	return latest.UTC()
}

func matchRecords(matches []ai.LLMMemoryMatch) []ai.LLMMemoryRecord {
	records := make([]ai.LLMMemoryRecord, len(matches))
	for i, m := range matches {
		records[i] = m.Record
	}
	return records
}

func filterCandidatesForEmbed(candidates []extractedMemory) []extractedMemory {
	result := make([]extractedMemory, 0, len(candidates))
	for _, c := range candidates {
		if c.Action == extractionActionNew || c.Action == extractionActionUpdate {
			result = append(result, c)
		}
	}
	return result
}

func (m *Module) runExtractionLLM(
	ctx context.Context,
	contextWindow extractionContext,
	existingMemories []ai.LLMMemoryRecord,
) ([]extractedMemory, error) {
	prompt := renderExtractionPrompt(contextWindow, existingMemories)
	extractCtx := ctx
	cancel := func() {}
	if m.cfg.ExtractionTimeout > 0 {
		extractCtx, cancel = context.WithTimeout(ctx, m.cfg.ExtractionTimeout)
	}
	defer cancel()

	stream, err := m.extractionProvider.GenerateStream(extractCtx, ai.LLMGenerateRequest{
		Model: m.cfg.ExtractionModel,
		Messages: []ai.LLMMessage{
			{Role: ai.LLMMessageRoleSystem, Content: extractionSystemPrompt},
			{Role: ai.LLMMessageRoleUser, Content: prompt},
		},
		Temperature: 0.1,
	})
	if err != nil {
		return nil, fmt.Errorf("extraction generate: %w", err)
	}

	responseText, err := collectStreamText(extractCtx, stream)
	closeErr := stream.Close()
	if err != nil {
		if closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close extraction stream: %w", closeErr))
		}
		return nil, err
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close extraction stream: %w", closeErr)
	}

	candidates, err := parseExtractionResponse(responseText)
	if err != nil {
		m.debugExtractionParseError(ctx, err, responseText)
		return nil, nil
	}

	return candidates, nil
}

func (m *Module) embedCandidatesBatch(
	ctx context.Context,
	candidates []extractedMemory,
) ([][]float32, error) {
	if len(candidates) == 0 {
		return nil, nil
	}

	texts := make([]string, len(candidates))
	for i, c := range candidates {
		texts[i] = strings.TrimSpace(buildEmbeddingText(c))
	}

	response, err := m.embeddingProvider.Embed(ctx, ai.EmbeddingRequest{
		Texts:    texts,
		TaskType: ai.EmbeddingTaskTypeDocument,
	})
	if err != nil {
		return nil, fmt.Errorf("embed candidates batch: %w", err)
	}
	if len(response.Vectors) != len(texts) {
		return nil, fmt.Errorf("embed candidates batch: expected %d vectors, got %d", len(texts), len(response.Vectors))
	}

	vectors := make([][]float32, len(response.Vectors))
	for i, v := range response.Vectors {
		vectors[i] = append([]float32(nil), v...)
	}
	return vectors, nil
}

// applyNew stores a brand-new memory record.
func (m *Module) applyNew(
	ctx context.Context,
	scope ai.LLMMemoryScope,
	contextWindow extractionContext,
	candidate extractedMemory,
	embedding []float32,
) error {
	profile := buildMemoryProfile(candidate, contextWindow, m.now())
	metadata := buildProfileMetadata(profile)

	_, err := m.llmMemory.Store(ctx, ai.LLMMemoryEntry{
		Scope:     scope,
		Content:   candidate.Content,
		Category:  candidate.Category,
		Embedding: embedding,
		Profile:   profile,
		Metadata:  metadata,
		Keywords:  candidate.Keywords,
		Tags:      candidate.Tags,
	})
	if err != nil {
		return fmt.Errorf("store new memory: %w", err)
	}
	return nil
}

// applyUpdate validates target_id against relevantExisting and updates the
// record. Falls through to applyNew if the target_id is invalid.
func (m *Module) applyUpdate(
	ctx context.Context,
	scope ai.LLMMemoryScope,
	contextWindow extractionContext,
	candidate extractedMemory,
	embedding []float32,
	relevantExisting []ai.LLMMemoryMatch,
) error {
	targetID := strings.TrimSpace(candidate.TargetID)
	if targetID == "" {
		return m.applyNew(ctx, scope, contextWindow, candidate, embedding)
	}

	var existingRecord ai.LLMMemoryRecord
	found := false
	for _, match := range relevantExisting {
		if match.Record.ID == targetID {
			existingRecord = match.Record
			found = true
			break
		}
	}
	if !found {
		return m.applyNew(ctx, scope, contextWindow, candidate, embedding)
	}

	profile := buildMemoryProfile(candidate, contextWindow, m.now())
	if existingRecord.Profile.Importance > profile.Importance {
		profile.Importance = existingRecord.Profile.Importance
	}
	metadata := buildProfileMetadata(profile)

	_, err := m.llmMemory.Update(ctx, ai.LLMMemoryUpdate{
		ID:        targetID,
		Content:   candidate.Content,
		Category:  candidate.Category,
		Embedding: embedding,
		Profile:   profile,
		Metadata:  metadata,
		Keywords:  candidate.Keywords,
		Tags:      candidate.Tags,
	})
	if err != nil {
		return fmt.Errorf("update memory %s: %w", targetID, err)
	}
	return nil
}

// applyDelete validates target_id against relevantExisting and deletes the
// record. No-op if the target_id is invalid.
func (m *Module) applyDelete(
	ctx context.Context,
	targetID string,
	relevantExisting []ai.LLMMemoryMatch,
) error {
	targetID = strings.TrimSpace(targetID)
	if targetID == "" {
		return nil
	}

	found := false
	for _, match := range relevantExisting {
		if match.Record.ID == targetID {
			found = true
			break
		}
	}
	if !found {
		return nil
	}

	if err := m.llmMemory.Delete(ctx, targetID); err != nil {
		return fmt.Errorf("delete memory %s: %w", targetID, err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Extraction response parsing
// ---------------------------------------------------------------------------

func parseExtractionResponse(text string) ([]extractedMemory, error) {
	trimmed := strings.TrimSpace(stripMarkdownCodeFence(text))
	if trimmed == "" {
		return nil, fmt.Errorf("empty extraction response")
	}

	var candidates []extractedMemory
	if err := json.Unmarshal([]byte(trimmed), &candidates); err != nil {
		extracted, extractErr := extractJSONArray(trimmed)
		if extractErr != nil {
			return nil, fmt.Errorf("parse extraction response: %w", err)
		}
		if err := json.Unmarshal([]byte(extracted), &candidates); err != nil {
			return nil, fmt.Errorf("parse extraction response: %w", err)
		}
	}

	valid := make([]extractedMemory, 0, len(candidates))
	for _, candidate := range candidates {
		candidate.Content = strings.TrimSpace(candidate.Content)
		candidate.Category = strings.TrimSpace(candidate.Category)
		candidate.Action = extractionAction(strings.TrimSpace(string(candidate.Action)))
		candidate.TargetID = strings.TrimSpace(candidate.TargetID)
		candidate.SubjectActorID = strings.TrimSpace(candidate.SubjectActorID)
		candidate.SubjectActorName = strings.TrimSpace(candidate.SubjectActorName)
		candidate.ValidUntil = strings.TrimSpace(candidate.ValidUntil)
		if candidate.ValidUntil != "" {
			if _, err := time.Parse(time.RFC3339, candidate.ValidUntil); err != nil {
				candidate.ValidUntil = ""
			}
		}

		switch candidate.Action {
		case extractionActionNew, extractionActionUpdate, extractionActionDelete:
		case extractionActionNoop, "":
			continue
		default:
			candidate.Action = extractionActionNew
		}

		if candidate.Action == extractionActionDelete {
			if candidate.TargetID == "" {
				continue
			}
			valid = append(valid, candidate)
			continue
		}

		if candidate.Content == "" {
			continue
		}
		if candidate.Importance < 1 || candidate.Importance > 10 {
			continue
		}
		if _, ok := validExtractionCategories[candidate.Category]; !ok {
			continue
		}
		if candidate.Action == extractionActionUpdate && candidate.TargetID == "" {
			candidate.Action = extractionActionNew
		}

		valid = append(valid, candidate)
	}

	return valid, nil
}

// ---------------------------------------------------------------------------
// Stream and embedding helpers
// ---------------------------------------------------------------------------

func collectStreamText(ctx context.Context, stream ai.LLMStream) (string, error) {
	var builder strings.Builder
	for {
		chunk, err := stream.Recv(ctx)
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return "", fmt.Errorf("collect stream text: %w", err)
		}

		switch chunk.Kind.Normalize() {
		case ai.LLMGenerateChunkKindOutputText:
			builder.WriteString(chunk.Delta)
		case ai.LLMGenerateChunkKindThinkingSummary,
			ai.LLMGenerateChunkKindToolCall:
			continue
		default:
			builder.WriteString(chunk.Delta)
		}
	}

	return strings.TrimSpace(builder.String()), nil
}

func embedSingleText(
	ctx context.Context,
	embeddingProvider ai.EmbeddingProvider,
	text string,
	taskType ai.EmbeddingTaskType,
) ([]float32, error) {
	if ctx == nil {
		return nil, fmt.Errorf("embed single text: nil context")
	}
	if embeddingProvider == nil {
		return nil, fmt.Errorf("embed single text: embedding provider is nil")
	}

	response, err := embeddingProvider.Embed(ctx, ai.EmbeddingRequest{
		Texts:    []string{strings.TrimSpace(text)},
		TaskType: taskType,
	})
	if err != nil {
		return nil, fmt.Errorf("embed single text: %w", err)
	}
	if len(response.Vectors) != 1 {
		return nil, fmt.Errorf("embed single text: expected 1 vector, got %d", len(response.Vectors))
	}

	return append([]float32(nil), response.Vectors[0]...), nil
}

// ---------------------------------------------------------------------------
// Text processing helpers
// ---------------------------------------------------------------------------

func stripMarkdownCodeFence(text string) string {
	trimmed := strings.TrimSpace(text)
	if !strings.HasPrefix(trimmed, "```") {
		return trimmed
	}

	trimmed = strings.TrimPrefix(trimmed, "```")
	if newline := strings.Index(trimmed, "\n"); newline >= 0 {
		trimmed = trimmed[newline+1:]
	}
	if end := strings.LastIndex(trimmed, "```"); end >= 0 {
		trimmed = trimmed[:end]
	}

	return strings.TrimSpace(trimmed)
}

func extractJSONArray(text string) (string, error) {
	start := strings.Index(text, "[")
	end := strings.LastIndex(text, "]")
	if start < 0 || end <= start {
		return "", fmt.Errorf("json array not found")
	}

	return strings.TrimSpace(text[start : end+1]), nil
}

func normalizeAnchorTime(event *platform.Event, fallback time.Time) time.Time {
	if event == nil || event.OccurredAt.IsZero() {
		return fallback.UTC()
	}

	return event.OccurredAt.UTC()
}

// ---------------------------------------------------------------------------
// Memory profile construction
// ---------------------------------------------------------------------------

func buildMemoryProfile(
	candidate extractedMemory,
	contextWindow extractionContext,
	now time.Time,
) ai.LLMMemoryProfile {
	profile := ai.LLMMemoryProfile{
		Kind:            ai.LLMMemoryKindUnit,
		Importance:      candidate.Importance,
		LastAccessedAt:  now.UTC(),
		AccessCount:     0,
		Source:          "natural",
		SourceArticleID: contextWindow.SourceArticleID,
		SourceActor:     memoryActorRef(contextWindow.SourceActor),
		SubjectActor:    resolveSubjectActor(candidate, contextWindow),
	}
	if candidate.ValidUntil != "" {
		if parsed, err := time.Parse(time.RFC3339, candidate.ValidUntil); err == nil {
			utc := parsed.UTC()
			profile.ValidUntil = &utc
		}
	}

	return profile
}

func resolveSubjectActor(candidate extractedMemory, contextWindow extractionContext) *ai.LLMMemoryActorRef {
	if strings.TrimSpace(candidate.SubjectActorID) == "" && strings.TrimSpace(candidate.SubjectActorName) == "" {
		if contextWindow.SourceActor.IsBot {
			return nil
		}
		return memoryActorRef(contextWindow.SourceActor)
	}

	for _, participant := range contextWindow.Participants {
		if strings.TrimSpace(candidate.SubjectActorID) != "" && participant.ID == strings.TrimSpace(candidate.SubjectActorID) {
			return cloneActorRef(&participant)
		}
		if strings.TrimSpace(candidate.SubjectActorName) != "" &&
			strings.EqualFold(strings.TrimSpace(participant.Name), strings.TrimSpace(candidate.SubjectActorName)) {
			return cloneActorRef(&participant)
		}
	}

	ref := &ai.LLMMemoryActorRef{
		ID:   strings.TrimSpace(candidate.SubjectActorID),
		Name: strings.TrimSpace(candidate.SubjectActorName),
	}
	if ref.ID == "" && ref.Name == "" {
		return nil
	}

	return ref
}

func memoryActorRef(actor platform.Actor) *ai.LLMMemoryActorRef {
	name := actorDisplayName(actor)
	id := strings.TrimSpace(actor.ID)
	if id == "" && name == "" {
		return nil
	}

	return &ai.LLMMemoryActorRef{
		ID:    id,
		Name:  name,
		IsBot: actor.IsBot,
	}
}

func actorDisplayName(actor platform.Actor) string {
	if name := strings.TrimSpace(actor.DisplayName); name != "" {
		return name
	}
	if name := strings.TrimSpace(actor.Username); name != "" {
		return name
	}
	if id := strings.TrimSpace(actor.ID); id != "" {
		return id
	}

	return ""
}

func buildProfileMetadata(profile ai.LLMMemoryProfile) map[string]string {
	metadata := map[string]string{
		ai.LLMMemoryMetadataAccessCount:  strconv.Itoa(profile.AccessCount),
		ai.LLMMemoryMetadataImportance:   strconv.Itoa(profile.Importance),
		ai.LLMMemoryMetadataLastAccessed: profile.LastAccessedAt.UTC().Format(time.RFC3339),
		ai.LLMMemoryMetadataSource:       strings.TrimSpace(profile.Source),
	}
	if sourceArticleID := strings.TrimSpace(profile.SourceArticleID); sourceArticleID != "" {
		metadata[ai.LLMMemoryMetadataSourceArticleID] = sourceArticleID
	}
	if profile.SourceActor != nil {
		if profile.SourceActor.ID != "" {
			metadata[ai.LLMMemoryMetadataSourceActorID] = profile.SourceActor.ID
		}
		if profile.SourceActor.Name != "" {
			metadata[ai.LLMMemoryMetadataSourceActorName] = profile.SourceActor.Name
		}
		metadata[ai.LLMMemoryMetadataSourceActorIsBot] = strconv.FormatBool(profile.SourceActor.IsBot)
	}
	if profile.SubjectActor != nil {
		if profile.SubjectActor.ID != "" {
			metadata[ai.LLMMemoryMetadataSubjectActorID] = profile.SubjectActor.ID
		}
		if profile.SubjectActor.Name != "" {
			metadata[ai.LLMMemoryMetadataSubjectActorName] = profile.SubjectActor.Name
		}
		metadata[ai.LLMMemoryMetadataSubjectActorIsBot] = strconv.FormatBool(profile.SubjectActor.IsBot)
	}

	return metadata
}

func cloneActorRef(actor *ai.LLMMemoryActorRef) *ai.LLMMemoryActorRef {
	if actor == nil {
		return nil
	}

	cloned := *actor
	return &cloned
}

func buildEmbeddingText(candidate extractedMemory) string {
	parts := []string{strings.TrimSpace(candidate.Content)}
	for _, keyword := range candidate.Keywords {
		if trimmed := strings.TrimSpace(keyword); trimmed != "" {
			parts = append(parts, trimmed)
		}
	}
	for _, tag := range candidate.Tags {
		if trimmed := strings.TrimSpace(tag); trimmed != "" {
			parts = append(parts, trimmed)
		}
	}

	return strings.Join(parts, " ")
}
