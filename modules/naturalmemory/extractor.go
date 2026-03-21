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
	"unicode/utf8"

	"ex-otogi/pkg/otogi/ai"
	"ex-otogi/pkg/otogi/core"
	"ex-otogi/pkg/otogi/platform"
)

const existingMemoryPromptLimit = 50

var validExtractionCategories = map[string]struct{}{
	"experience": {},
	"knowledge":  {},
	"preference": {},
	"reflection": {},
	"user_fact":  {},
}

type extractedMemory struct {
	Content          string   `json:"content"`
	Category         string   `json:"category"`
	Importance       int      `json:"importance"`
	SubjectActorID   string   `json:"subject_actor_id"`
	SubjectActorName string   `json:"subject_actor_name"`
	ValidUntil       string   `json:"valid_until"`
	Keywords         []string `json:"keywords"`
	Tags             []string `json:"tags"`
}

type extractionContext struct {
	ConversationText string
	AnchorTime       time.Time
	SourceArticleID  string
	SourceActor      platform.Actor
	Participants     []ai.LLMMemoryActorRef
}

func (m *Module) buildExtractionContext(ctx context.Context, event *platform.Event) (extractionContext, error) {
	if event == nil || event.Article == nil {
		return extractionContext{}, nil
	}
	if m == nil || m.memory == nil {
		return extractionContext{}, fmt.Errorf("memory service unavailable")
	}

	anchorTime := normalizeAnchorTime(event, m.now())
	query := core.ConversationContextBeforeQuery{
		TenantID:         event.TenantID,
		Platform:         event.Source.Platform,
		ConversationID:   event.Conversation.ID,
		ThreadID:         event.Article.ThreadID,
		AnchorArticleID:  event.Article.ID,
		AnchorOccurredAt: anchorTime,
		BeforeLimit:      m.cfg.ContextWindowSize,
		ExcludeArticleIDs: []string{
			event.Article.ID,
		},
	}
	entries, err := m.memory.ListConversationContextBefore(ctx, query)
	if err != nil {
		return extractionContext{}, fmt.Errorf("list conversation context before: %w", err)
	}

	current := extractionConversationEntry{
		Actor:     event.Actor,
		Article:   *event.Article,
		CreatedAt: anchorTime,
	}

	return extractionContext{
		ConversationText: serializeExtractionConversation(entries, current, m.cfg.ExtractionMaxInputRunes),
		AnchorTime:       anchorTime,
		SourceArticleID:  strings.TrimSpace(event.Article.ID),
		SourceActor:      event.Actor,
		Participants:     buildExtractionParticipants(entries, current),
	}, nil
}

func (m *Module) extractMemories(ctx context.Context, scope ai.LLMMemoryScope, contextWindow extractionContext) error {
	if strings.TrimSpace(contextWindow.ConversationText) == "" {
		return nil
	}
	if m == nil || m.llmMemory == nil {
		return fmt.Errorf("llm memory service unavailable")
	}
	if m.extractionProvider == nil {
		return fmt.Errorf("extraction provider unavailable")
	}

	existing, err := m.llmMemory.ListByScope(ctx, scope, existingMemoryPromptLimit)
	if err != nil {
		return fmt.Errorf("list existing memories: %w", err)
	}
	m.debugExtractionStart(ctx, scope, contextWindow.SourceArticleID, len([]rune(contextWindow.ConversationText)), len(existing))

	prompt := renderExtractionPrompt(contextWindow, existing)
	extractionCtx := ctx
	cancel := func() {}
	if m.cfg.ExtractionTimeout > 0 {
		extractionCtx, cancel = context.WithTimeout(ctx, m.cfg.ExtractionTimeout)
	}
	defer cancel()

	stream, err := m.extractionProvider.GenerateStream(extractionCtx, ai.LLMGenerateRequest{
		Model: m.cfg.ExtractionModel,
		Messages: []ai.LLMMessage{
			{Role: ai.LLMMessageRoleSystem, Content: extractionSystemPrompt},
			{Role: ai.LLMMessageRoleUser, Content: prompt},
		},
		Temperature: 0.1,
	})
	if err != nil {
		return fmt.Errorf("extraction generate: %w", err)
	}

	responseText, err := collectStreamText(extractionCtx, stream)
	closeErr := stream.Close()
	if err != nil {
		if closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close extraction stream: %w", closeErr))
		}
		return err
	}
	if closeErr != nil {
		return fmt.Errorf("close extraction stream: %w", closeErr)
	}

	candidates, err := parseExtractionResponse(responseText)
	if err != nil {
		m.debugExtractionParseError(ctx, err, responseText)
		return nil
	}
	categories := make([]string, 0, len(candidates))
	for _, c := range candidates {
		categories = append(categories, c.Category)
	}
	m.debugExtractionResult(ctx, len(candidates), categories)

	for _, candidate := range candidates {
		if err := m.processCandidate(ctx, scope, contextWindow, candidate); err != nil {
			m.debugCandidateError(ctx, candidate, err)
		}
	}

	return nil
}

func (m *Module) processCandidate(
	ctx context.Context,
	scope ai.LLMMemoryScope,
	contextWindow extractionContext,
	candidate extractedMemory,
) error {
	profile := buildMemoryProfile(candidate, contextWindow, m.now())

	return m.upsertCandidate(ctx, scope, contextWindow, candidate, profile)
}

func (m *Module) upsertCandidate(
	ctx context.Context,
	scope ai.LLMMemoryScope,
	contextWindow extractionContext,
	candidate extractedMemory,
	profile ai.LLMMemoryProfile,
) error {
	if strings.TrimSpace(candidate.Content) == "" {
		return nil
	}
	if m == nil || m.embeddingProvider == nil {
		return fmt.Errorf("embedding provider unavailable")
	}
	if m.llmMemory == nil {
		return fmt.Errorf("llm memory service unavailable")
	}

	embedding, err := embedSingleText(ctx, m.embeddingProvider, buildEmbeddingText(candidate), ai.EmbeddingTaskTypeDocument)
	if err != nil {
		return fmt.Errorf("embed candidate: %w", err)
	}
	matches, err := m.llmMemory.Search(ctx, ai.LLMMemoryQuery{
		Scope:         scope,
		Embedding:     embedding,
		Limit:         m.cfg.SynthesisMatchLimit,
		MinSimilarity: m.cfg.DuplicateSimilarityThreshold * 0.8,
	})
	if err != nil {
		return fmt.Errorf("search duplicates: %w", err)
	}

	var topSimilarity float32
	if len(matches) > 0 {
		topSimilarity = matches[0].Similarity
	}
	m.debugSynthesisQuery(ctx, len([]rune(candidate.Content)), len(matches), topSimilarity)

	decision := m.decideSynthesis(ctx, candidate, matches, contextWindow)
	m.debugCandidateUpsert(ctx, decision.Action, decision.TargetID, candidate.Category, candidate.Importance, len([]rune(candidate.Content)))
	originalContent := candidate.Content
	switch decision.Action {
	case synthesisActionAdd, synthesisActionRewrite, synthesisActionSupersede:
		candidate.Content = strings.TrimSpace(decision.Content)
		candidate.Category = strings.TrimSpace(decision.Category)
		candidate.Importance = decision.Importance
		candidate.SubjectActorID = strings.TrimSpace(decision.SubjectActorID)
		candidate.SubjectActorName = strings.TrimSpace(decision.SubjectActorName)
	default:
	}

	canonicalEmbedding := embedding
	if (decision.Action == synthesisActionAdd || decision.Action == synthesisActionRewrite || decision.Action == synthesisActionSupersede) &&
		candidate.Content != "" && candidate.Content != originalContent {
		canonicalEmbedding, err = embedSingleText(ctx, m.embeddingProvider, buildEmbeddingText(candidate), ai.EmbeddingTaskTypeDocument)
		if err != nil {
			return fmt.Errorf("embed canonical candidate: %w", err)
		}
	}

	profile.Importance = candidate.Importance
	profile.SubjectActor = resolveSubjectActor(candidate, contextWindow)
	metadata := buildProfileMetadata(profile)

	var resultRecord ai.LLMMemoryRecord
	switch decision.Action {
	case synthesisActionAdd:
		stored, storeErr := m.llmMemory.Store(ctx, ai.LLMMemoryEntry{
			Scope:     scope,
			Content:   candidate.Content,
			Category:  candidate.Category,
			Embedding: canonicalEmbedding,
			Profile:   profile,
			Metadata:  metadata,
			Keywords:  candidate.Keywords,
			Tags:      candidate.Tags,
		})
		if storeErr != nil {
			return fmt.Errorf("store candidate: %w", storeErr)
		}
		resultRecord = stored
	case synthesisActionRewrite:
		existingRecord, found := findMatchRecord(matches, decision.TargetID)
		if !found {
			stored, storeErr := m.llmMemory.Store(ctx, ai.LLMMemoryEntry{
				Scope:     scope,
				Content:   candidate.Content,
				Category:  candidate.Category,
				Embedding: canonicalEmbedding,
				Profile:   profile,
				Metadata:  metadata,
				Keywords:  candidate.Keywords,
				Tags:      candidate.Tags,
			})
			if storeErr != nil {
				return fmt.Errorf("store rewritten candidate fallback: %w", storeErr)
			}
			resultRecord = stored
			break
		}
		profile = mergeSynthesizedProfile(existingRecord.Profile, profile, decision.AbsorbedRecordIDs)
		metadata = buildProfileMetadata(profile)
		update := ai.LLMMemoryUpdate{
			ID:        decision.TargetID,
			Content:   candidate.Content,
			Category:  candidate.Category,
			Embedding: canonicalEmbedding,
			Profile:   profile,
			Metadata:  mergeMetadataMaps(existingRecord.Metadata, metadata),
			Keywords:  candidate.Keywords,
			Tags:      candidate.Tags,
			Links:     existingRecord.Links,
		}
		updated, updateErr := m.llmMemory.Update(ctx, update)
		if updateErr != nil {
			return fmt.Errorf("update candidate %s: %w", decision.TargetID, updateErr)
		}
		for _, recordID := range decision.AbsorbedRecordIDs {
			if recordID == decision.TargetID {
				continue
			}
			if err := m.llmMemory.Delete(ctx, recordID); err != nil {
				return fmt.Errorf("delete absorbed candidate %s: %w", recordID, err)
			}
		}
		resultRecord = updated
	case synthesisActionSupersede:
		if err := m.llmMemory.Delete(ctx, decision.TargetID); err != nil {
			return fmt.Errorf("delete superseded memory %s: %w", decision.TargetID, err)
		}
		for _, recordID := range decision.AbsorbedRecordIDs {
			if recordID == decision.TargetID {
				continue
			}
			if err := m.llmMemory.Delete(ctx, recordID); err != nil {
				return fmt.Errorf("delete absorbed candidate %s: %w", recordID, err)
			}
		}
		stored, storeErr := m.llmMemory.Store(ctx, ai.LLMMemoryEntry{
			Scope:     scope,
			Content:   candidate.Content,
			Category:  candidate.Category,
			Embedding: canonicalEmbedding,
			Profile:   profile,
			Metadata:  metadata,
			Keywords:  candidate.Keywords,
			Tags:      candidate.Tags,
		})
		if storeErr != nil {
			return fmt.Errorf("store superseding candidate: %w", storeErr)
		}
		resultRecord = stored
	case synthesisActionNoop:
		return nil
	default:
		return fmt.Errorf("unsupported synthesis action %q", decision.Action)
	}

	m.applyLinks(ctx, resultRecord, matches, decision.Action, decision.TargetID, decision.AbsorbedRecordIDs)

	return nil
}

func findMatchRecord(matches []ai.LLMMemoryMatch, targetID string) (ai.LLMMemoryRecord, bool) {
	for _, match := range matches {
		if match.Record.ID == targetID {
			return match.Record, true
		}
	}

	return ai.LLMMemoryRecord{}, false
}

func cloneMetadataMap(input map[string]string) map[string]string {
	if len(input) == 0 {
		return nil
	}

	cloned := make(map[string]string, len(input))
	for key, value := range input {
		cloned[key] = value
	}

	return cloned
}

func mergeMetadataMaps(existing map[string]string, generated map[string]string) map[string]string {
	if len(existing) == 0 {
		return cloneMetadataMap(generated)
	}

	merged := cloneMetadataMap(existing)
	if merged == nil {
		merged = make(map[string]string, len(generated))
	}
	for key, value := range generated {
		merged[key] = value
	}

	return merged
}

type extractionConversationEntry struct {
	Actor     platform.Actor
	Article   platform.Article
	CreatedAt time.Time
}

func serializeExtractionConversation(
	entries []core.ConversationContextEntry,
	current extractionConversationEntry,
	maxRunes int,
) string {
	if maxRunes <= 0 {
		return ""
	}

	currentLine := formatExtractionLine(current.Actor, current.CreatedAt, current.Article.Text)
	if currentLine == "" {
		return ""
	}
	if runeCount(currentLine) >= maxRunes {
		return trimRunesWithEllipsis(currentLine, maxRunes)
	}

	lines := make([]string, 0, len(entries))
	for _, entry := range entries {
		line := formatExtractionLine(entry.Actor, entry.CreatedAt, entry.Article.Text)
		if line == "" {
			continue
		}
		lines = append(lines, line)
	}

	selected := []string{currentLine}
	used := runeCount(currentLine)
	omitted := 0
	for index := len(lines) - 1; index >= 0; index-- {
		lineRunes := runeCount(lines[index]) + 1
		if used+lineRunes > maxRunes {
			omitted = index + 1
			break
		}
		selected = append([]string{lines[index]}, selected...)
		used += lineRunes
	}

	serialized := strings.Join(selected, "\n")
	if omitted > 0 {
		const prefix = "[...]\n"
		if runeCount(prefix)+runeCount(serialized) <= maxRunes {
			serialized = prefix + serialized
		}
	}

	return trimRunesWithEllipsis(serialized, maxRunes)
}

func buildExtractionParticipants(
	entries []core.ConversationContextEntry,
	current extractionConversationEntry,
) []ai.LLMMemoryActorRef {
	seen := make(map[string]struct{}, len(entries)+1)
	participants := make([]ai.LLMMemoryActorRef, 0, len(entries)+1)
	appendActor := func(actor platform.Actor) {
		ref := memoryActorRef(actor)
		if ref == nil {
			return
		}

		key := strings.TrimSpace(ref.ID) + "\x00" + strings.ToLower(strings.TrimSpace(ref.Name))
		if _, exists := seen[key]; exists {
			return
		}
		seen[key] = struct{}{}
		participants = append(participants, *ref)
	}

	for _, entry := range entries {
		appendActor(entry.Actor)
	}
	appendActor(current.Actor)

	return participants
}

func formatExtractionLine(actor platform.Actor, createdAt time.Time, text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return ""
	}

	speaker := speakerLabel(actor)
	if actorID := strings.TrimSpace(actor.ID); actorID != "" {
		speaker += " <actor:" + actorID + ">"
	}
	if actor.IsBot {
		speaker += " (bot)"
	}

	prefix := ""
	if !createdAt.IsZero() {
		prefix = "[" + createdAt.UTC().Format(time.RFC3339) + "] "
	}

	return prefix + speaker + ": " + strings.Join(strings.Fields(trimmed), " ")
}

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
		candidate.SubjectActorID = strings.TrimSpace(candidate.SubjectActorID)
		candidate.SubjectActorName = strings.TrimSpace(candidate.SubjectActorName)
		candidate.ValidUntil = strings.TrimSpace(candidate.ValidUntil)
		if candidate.ValidUntil != "" {
			if _, err := time.Parse(time.RFC3339, candidate.ValidUntil); err != nil {
				candidate.ValidUntil = ""
			}
		}
		switch {
		case candidate.Content == "":
			continue
		case candidate.Importance < 1 || candidate.Importance > 10:
			continue
		}
		if _, ok := validExtractionCategories[candidate.Category]; !ok {
			continue
		}
		valid = append(valid, candidate)
	}

	return valid, nil
}

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

func speakerLabel(actor platform.Actor) string {
	if name := actorDisplayName(actor); name != "" {
		return name
	}
	if id := strings.TrimSpace(actor.ID); id != "" {
		return id
	}

	return "unknown"
}

func trimRunesWithEllipsis(raw string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}

	runes := []rune(raw)
	if len(runes) <= maxRunes {
		return raw
	}
	if maxRunes <= 3 {
		return strings.Repeat(".", maxRunes)
	}

	return string(runes[:maxRunes-3]) + "..."
}

func runeCount(value string) int {
	return utf8.RuneCountInString(value)
}

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
	if len(profile.EvidenceRecordIDs) > 0 {
		metadata[ai.LLMMemoryMetadataSourceRecordIDs] = strings.Join(profile.EvidenceRecordIDs, ",")
	}

	return metadata
}

func mergeObservedProfile(existing ai.LLMMemoryProfile, observed ai.LLMMemoryProfile) ai.LLMMemoryProfile {
	merged := existing
	if merged.Kind == "" {
		merged.Kind = observed.Kind
	}
	if observed.Importance > merged.Importance {
		merged.Importance = observed.Importance
	}
	if observed.LastAccessedAt.After(merged.LastAccessedAt) {
		merged.LastAccessedAt = observed.LastAccessedAt
	}
	if observed.AccessCount > merged.AccessCount {
		merged.AccessCount = observed.AccessCount
	}
	if strings.TrimSpace(merged.Source) == "" {
		merged.Source = observed.Source
	}
	if strings.TrimSpace(merged.SourceArticleID) == "" {
		merged.SourceArticleID = observed.SourceArticleID
	}
	if merged.SourceActor == nil {
		merged.SourceActor = cloneActorRef(observed.SourceActor)
	}
	if merged.SubjectActor == nil {
		merged.SubjectActor = cloneActorRef(observed.SubjectActor)
	}
	if len(merged.EvidenceRecordIDs) == 0 && len(observed.EvidenceRecordIDs) > 0 {
		merged.EvidenceRecordIDs = append([]string(nil), observed.EvidenceRecordIDs...)
	}

	return merged
}

func mergeSynthesizedProfile(
	existing ai.LLMMemoryProfile,
	observed ai.LLMMemoryProfile,
	absorbedRecordIDs []string,
) ai.LLMMemoryProfile {
	merged := mergeObservedProfile(existing, observed)
	merged.Kind = ai.LLMMemoryKindSynthesized
	merged.Importance = maxInt(merged.Importance, observed.Importance)
	merged.EvidenceRecordIDs = uniqueRecordIDs(merged.EvidenceRecordIDs, absorbedRecordIDs)

	return merged
}

func cloneActorRef(actor *ai.LLMMemoryActorRef) *ai.LLMMemoryActorRef {
	if actor == nil {
		return nil
	}

	cloned := *actor
	return &cloned
}

func uniqueRecordIDs(groups ...[]string) []string {
	seen := make(map[string]struct{})
	merged := make([]string, 0)
	for _, group := range groups {
		for _, recordID := range group {
			trimmed := strings.TrimSpace(recordID)
			if trimmed == "" {
				continue
			}
			if _, exists := seen[trimmed]; exists {
				continue
			}
			seen[trimmed] = struct{}{}
			merged = append(merged, trimmed)
		}
	}
	if len(merged) == 0 {
		return nil
	}

	return merged
}

func maxInt(left int, right int) int {
	if left > right {
		return left
	}

	return right
}

const (
	linkRelationRelated = "related"
	linkRelationRefines = "refines"
	linkMaxPerRecord    = 3
	linkMinSimilarity   = float32(0.5)
)

// generateLinks creates directional links from a newly stored or updated
// record to the most similar existing records in the match set. Records that
// were deleted during the upsert (target of supersede or absorbed records)
// are excluded.
func generateLinks(
	matches []ai.LLMMemoryMatch,
	action synthesisAction,
	targetID string,
	absorbedIDs []string,
	threshold float32,
	maxLinks int,
) []ai.LLMMemoryLink {
	if len(matches) == 0 || maxLinks <= 0 {
		return nil
	}

	excluded := make(map[string]struct{}, len(absorbedIDs)+1)
	if targetID != "" && action == synthesisActionSupersede {
		excluded[targetID] = struct{}{}
	}
	for _, id := range absorbedIDs {
		excluded[id] = struct{}{}
	}

	relation := linkRelationRelated
	if action == synthesisActionRewrite {
		relation = linkRelationRefines
	}

	var links []ai.LLMMemoryLink
	for _, match := range matches {
		if _, skip := excluded[match.Record.ID]; skip {
			continue
		}
		if match.Similarity < threshold {
			continue
		}
		if match.Record.ID == targetID {
			continue
		}
		links = append(links, ai.LLMMemoryLink{
			TargetID: match.Record.ID,
			Relation: relation,
		})
		if len(links) >= maxLinks {
			break
		}
	}

	return links
}

// applyLinks generates links for a newly stored or updated record and sets
// them via an Update call. It also creates reverse links on target records.
// Errors are non-fatal and silently ignored.
func (m *Module) applyLinks(
	ctx context.Context,
	record ai.LLMMemoryRecord,
	matches []ai.LLMMemoryMatch,
	action synthesisAction,
	targetID string,
	absorbedIDs []string,
) {
	if m == nil || m.llmMemory == nil || record.ID == "" {
		return
	}

	links := generateLinks(matches, action, targetID, absorbedIDs, linkMinSimilarity, linkMaxPerRecord)
	if len(links) == 0 {
		return
	}

	record.Links = mergeLinks(record.Links, links)
	if _, err := m.llmMemory.Update(ctx, ai.LLMMemoryUpdate{
		ID:        record.ID,
		Content:   record.Content,
		Category:  record.Category,
		Embedding: append([]float32(nil), record.Embedding...),
		Profile:   record.Profile,
		Metadata:  cloneMetadataMap(record.Metadata),
		Keywords:  append([]string(nil), record.Keywords...),
		Tags:      append([]string(nil), record.Tags...),
		Links:     record.Links,
	}); err != nil {
		return
	}

	// Add reverse links on each target.
	reverseLinksApplied := 0
	reverseLink := ai.LLMMemoryLink{TargetID: record.ID, Relation: links[0].Relation}
	for _, link := range links {
		for _, match := range matches {
			if match.Record.ID != link.TargetID {
				continue
			}
			updatedLinks := mergeLinks(match.Record.Links, []ai.LLMMemoryLink{reverseLink})
			if _, err := m.llmMemory.Update(ctx, ai.LLMMemoryUpdate{
				ID:        match.Record.ID,
				Content:   match.Record.Content,
				Category:  match.Record.Category,
				Embedding: append([]float32(nil), match.Record.Embedding...),
				Profile:   match.Record.Profile,
				Metadata:  cloneMetadataMap(match.Record.Metadata),
				Keywords:  append([]string(nil), match.Record.Keywords...),
				Tags:      append([]string(nil), match.Record.Tags...),
				Links:     updatedLinks,
			}); err != nil {
				continue
			}
			reverseLinksApplied++
			break
		}
	}
	m.debugLinkGeneration(ctx, record.ID, len(links), reverseLinksApplied)
}

// mergeLinks unions two link slices, deduplicating by TargetID.
func mergeLinks(existing []ai.LLMMemoryLink, additions []ai.LLMMemoryLink) []ai.LLMMemoryLink {
	seen := make(map[string]struct{}, len(existing))
	merged := make([]ai.LLMMemoryLink, 0, len(existing)+len(additions))
	for _, link := range existing {
		seen[link.TargetID] = struct{}{}
		merged = append(merged, link)
	}
	for _, link := range additions {
		if _, exists := seen[link.TargetID]; exists {
			continue
		}
		seen[link.TargetID] = struct{}{}
		merged = append(merged, link)
	}
	if len(merged) == 0 {
		return nil
	}

	return merged
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
