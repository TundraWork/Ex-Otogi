package naturalmemory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"ex-otogi/pkg/otogi/ai"
)

const reflectionSystemPrompt = `You are a memory reflection system. Synthesize a small number of higher-level memories from strong long-term memories in one conversation.`

type reflectionCandidate struct {
	Content          string   `json:"content"`
	Importance       int      `json:"importance"`
	SubjectActorID   string   `json:"subject_actor_id"`
	SubjectActorName string   `json:"subject_actor_name"`
	SourceRecordIDs  []string `json:"source_record_ids"`
}

func (m *Module) maybeGenerateReflections(
	ctx context.Context,
	scope ai.LLMMemoryScope,
	records []ai.LLMMemoryRecord,
	now time.Time,
) error {
	if m == nil || m.consolidationProvider == nil || m.cfg.ReflectionMaxGenerated <= 0 {
		return nil
	}

	sourceRecords := selectReflectionSourceRecords(records, m.cfg.ReflectionSourceLimit, m.cfg.DecayFactor, now)
	if len(sourceRecords) < m.cfg.ReflectionMinSourceMemories {
		return nil
	}

	candidates, err := m.generateReflectionCandidates(ctx, sourceRecords)
	if err != nil {
		return err
	}
	if len(candidates) == 0 {
		return nil
	}

	contextWindow := extractionContext{
		AnchorTime:   now,
		Participants: participantsFromRecords(sourceRecords),
	}
	for _, candidate := range candidates {
		profile := ai.LLMMemoryProfile{
			Kind:              ai.LLMMemoryKindSynthesized,
			Importance:        candidate.Importance,
			LastAccessedAt:    now.UTC(),
			AccessCount:       0,
			Source:            "natural.reflection",
			EvidenceRecordIDs: uniqueRecordIDs(candidate.SourceRecordIDs),
		}
		if strings.TrimSpace(candidate.SubjectActorID) != "" || strings.TrimSpace(candidate.SubjectActorName) != "" {
			profile.SubjectActor = resolveSubjectActor(extractedMemory{
				SubjectActorID:   candidate.SubjectActorID,
				SubjectActorName: candidate.SubjectActorName,
			}, contextWindow)
		}
		err := m.upsertCandidate(ctx, scope, contextWindow, extractedMemory{
			Content:          strings.TrimSpace(candidate.Content),
			Category:         "reflection",
			Importance:       candidate.Importance,
			SubjectActorID:   strings.TrimSpace(candidate.SubjectActorID),
			SubjectActorName: strings.TrimSpace(candidate.SubjectActorName),
		}, profile)
		if err != nil && m.logger != nil {
			m.logger.WarnContext(ctx, "naturalmemory reflection candidate",
				"error", err,
				"content_runes", len([]rune(candidate.Content)),
			)
		}
	}

	return nil
}

func (m *Module) generateReflectionCandidates(
	ctx context.Context,
	records []ai.LLMMemoryRecord,
) ([]reflectionCandidate, error) {
	requestCtx := ctx
	cancel := func() {}
	if m.cfg.ConsolidationTimeout > 0 {
		requestCtx, cancel = context.WithTimeout(ctx, m.cfg.ConsolidationTimeout)
	}
	defer cancel()

	stream, err := m.consolidationProvider.GenerateStream(requestCtx, ai.LLMGenerateRequest{
		Model: m.cfg.ConsolidationModel,
		Messages: []ai.LLMMessage{
			{Role: ai.LLMMessageRoleSystem, Content: reflectionSystemPrompt},
			{Role: ai.LLMMessageRoleUser, Content: renderReflectionPrompt(records, m.cfg.ReflectionMaxGenerated)},
		},
		Temperature: 0.2,
	})
	if err != nil {
		return nil, fmt.Errorf("reflection generate: %w", err)
	}

	responseText, err := collectStreamText(requestCtx, stream)
	closeErr := stream.Close()
	if err != nil {
		if closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close reflection stream: %w", closeErr))
		}
		return nil, err
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close reflection stream: %w", closeErr)
	}

	candidates, err := parseReflectionResponse(responseText, m.cfg.ReflectionMaxGenerated)
	if err != nil {
		return nil, err
	}

	return candidates, nil
}

func renderReflectionPrompt(records []ai.LLMMemoryRecord, maxGenerated int) string {
	var builder strings.Builder

	builder.WriteString("Review the strong memories below and synthesize higher-level reflection memories.\n\n")
	builder.WriteString("Rules:\n")
	builder.WriteString("- Each reflection must combine or summarize multiple source memories\n")
	builder.WriteString("- Do not restate one source memory verbatim\n")
	builder.WriteString("- Use category reflection implicitly; do not include category in the output\n")
	builder.WriteString("- Keep each reflection self-contained and explicit about the subject when possible\n")
	builder.WriteString(fmt.Sprintf("- Generate at most %d reflections\n\n", maxGenerated))
	builder.WriteString("<memories>\n")
	for _, record := range records {
		builder.WriteString(fmt.Sprintf(
			"<memory id=%q importance=%d kind=%q category=%q>\n%s\n</memory>\n",
			record.ID,
			memoryImportance(record),
			record.Profile.Kind,
			record.Category,
			record.Content,
		))
	}
	builder.WriteString("</memories>\n\n")
	builder.WriteString(`Respond with a JSON array only. Format: [{"content":"...","importance":8,"subject_actor_id":"...","subject_actor_name":"...","source_record_ids":["mem-1","mem-2"]}]`)

	return builder.String()
}

func parseReflectionResponse(text string, maxGenerated int) ([]reflectionCandidate, error) {
	trimmed := strings.TrimSpace(stripMarkdownCodeFence(text))
	if trimmed == "" {
		return nil, fmt.Errorf("empty reflection response")
	}

	var candidates []reflectionCandidate
	if err := json.Unmarshal([]byte(trimmed), &candidates); err != nil {
		extracted, extractErr := extractJSONArray(trimmed)
		if extractErr != nil {
			return nil, fmt.Errorf("parse reflection response: %w", err)
		}
		if err := json.Unmarshal([]byte(extracted), &candidates); err != nil {
			return nil, fmt.Errorf("parse reflection response: %w", err)
		}
	}

	valid := make([]reflectionCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		candidate.Content = strings.TrimSpace(candidate.Content)
		candidate.SubjectActorID = strings.TrimSpace(candidate.SubjectActorID)
		candidate.SubjectActorName = strings.TrimSpace(candidate.SubjectActorName)
		candidate.SourceRecordIDs = uniqueRecordIDs(candidate.SourceRecordIDs)
		if candidate.Content == "" || candidate.Importance < 1 || candidate.Importance > 10 || len(candidate.SourceRecordIDs) == 0 {
			continue
		}
		valid = append(valid, candidate)
		if maxGenerated > 0 && len(valid) >= maxGenerated {
			break
		}
	}

	return valid, nil
}

func selectReflectionSourceRecords(
	records []ai.LLMMemoryRecord,
	limit int,
	decayFactor float64,
	now time.Time,
) []ai.LLMMemoryRecord {
	filtered := make([]ai.LLMMemoryRecord, 0, len(records))
	for _, record := range records {
		if record.Category == "reflection" {
			continue
		}
		filtered = append(filtered, record)
	}
	sortMemoriesByScore(filtered, decayFactor, now)
	if limit > 0 && len(filtered) > limit {
		filtered = filtered[:limit]
	}

	return filtered
}

func participantsFromRecords(records []ai.LLMMemoryRecord) []ai.LLMMemoryActorRef {
	seen := make(map[string]struct{})
	participants := make([]ai.LLMMemoryActorRef, 0, len(records))
	appendActor := func(actor *ai.LLMMemoryActorRef) {
		if actor == nil {
			return
		}
		key := strings.TrimSpace(actor.ID) + "\x00" + strings.ToLower(strings.TrimSpace(actor.Name))
		if _, exists := seen[key]; exists {
			return
		}
		seen[key] = struct{}{}
		participants = append(participants, *cloneActorRef(actor))
	}
	for _, record := range records {
		appendActor(record.Profile.SourceActor)
		appendActor(record.Profile.SubjectActor)
	}

	return participants
}

func sortMemoriesByScore(records []ai.LLMMemoryRecord, decayFactor float64, now time.Time) {
	sort.Slice(records, func(i, j int) bool {
		leftScore := effectiveScore(records[i], decayFactor, now)
		rightScore := effectiveScore(records[j], decayFactor, now)
		if leftScore == rightScore {
			return records[i].UpdatedAt.After(records[j].UpdatedAt)
		}
		return leftScore > rightScore
	})
}
