package naturalmemory

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"ex-otogi/pkg/otogi/ai"
)

const synthesisDecisionSystemPrompt = `You are a memory synthesis system. Decide whether a candidate memory should be added, rewritten into an existing canonical memory, or ignored as already covered.`

type synthesisAction string

const (
	synthesisActionAdd       synthesisAction = "add"
	synthesisActionRewrite   synthesisAction = "rewrite"
	synthesisActionSupersede synthesisAction = "supersede"
	synthesisActionNoop      synthesisAction = "noop"
)

type synthesisDecision struct {
	Action            synthesisAction `json:"action"`
	TargetID          string          `json:"target_id"`
	Content           string          `json:"content"`
	Category          string          `json:"category"`
	Importance        int             `json:"importance"`
	SubjectActorID    string          `json:"subject_actor_id"`
	SubjectActorName  string          `json:"subject_actor_name"`
	AbsorbedRecordIDs []string        `json:"absorbed_record_ids"`
}

func (m *Module) decideSynthesis(
	ctx context.Context,
	candidate extractedMemory,
	matches []ai.LLMMemoryMatch,
	contextWindow extractionContext,
) synthesisDecision {
	fallback := fallbackSynthesisDecision(candidate, matches, m.cfg.DuplicateSimilarityThreshold)
	if len(matches) == 0 || m == nil || m.extractionProvider == nil {
		return fallback
	}

	requestCtx := ctx
	cancel := func() {}
	if m.cfg.ExtractionTimeout > 0 {
		requestCtx, cancel = context.WithTimeout(ctx, m.cfg.ExtractionTimeout)
	}
	defer cancel()

	stream, err := m.extractionProvider.GenerateStream(requestCtx, ai.LLMGenerateRequest{
		Model: m.cfg.ExtractionModel,
		Messages: []ai.LLMMessage{
			{Role: ai.LLMMessageRoleSystem, Content: synthesisDecisionSystemPrompt},
			{Role: ai.LLMMessageRoleUser, Content: renderSynthesisDecisionPrompt(candidate, matches, contextWindow)},
		},
		Temperature: 0.1,
	})
	if err != nil {
		return fallback
	}

	responseText, err := collectStreamText(requestCtx, stream)
	closeErr := stream.Close()
	if err != nil || closeErr != nil {
		return fallback
	}

	decision, err := parseSynthesisDecision(responseText)
	if err != nil {
		return fallback
	}

	return normalizeSynthesisDecision(decision, candidate, matches, fallback)
}

func renderSynthesisDecisionPrompt(
	candidate extractedMemory,
	matches []ai.LLMMemoryMatch,
	contextWindow extractionContext,
) string {
	var builder strings.Builder

	builder.WriteString("You are deciding how to handle one candidate long-term memory.\n\n")
	builder.WriteString("Actions:\n")
	builder.WriteString("- add: the candidate is a genuinely new fact worth storing as its own memory\n")
	builder.WriteString("- rewrite: update one existing memory into a better canonical form and optionally absorb duplicates\n")
	builder.WriteString("- supersede: the candidate contradicts or invalidates an existing memory — delete the old and store the new\n")
	builder.WriteString("- noop: the candidate is already fully covered and should not change storage\n\n")
	builder.WriteString("Rules:\n")
	builder.WriteString("- Prefer rewrite over add when the candidate is a refinement, correction, or stronger phrasing of an existing memory\n")
	builder.WriteString("- Use supersede when the candidate directly contradicts an existing memory (e.g. preference changed, fact reversed, status updated to opposite)\n")
	builder.WriteString("- Rewritten or superseding content must be self-contained, explicit about the subject, and use absolute dates when needed\n")
	builder.WriteString("- target_id must be one of the existing memory ids below when action is rewrite or supersede\n")
	builder.WriteString("- absorbed_record_ids may include duplicate ids other than target_id\n\n")
	builder.WriteString("<anchor_time>\n")
	builder.WriteString(contextWindow.AnchorTime.UTC().Format(time.RFC3339))
	builder.WriteString("\n</anchor_time>\n\n")
	builder.WriteString("<candidate>\n")
	builder.WriteString(fmt.Sprintf("content: %s\n", candidate.Content))
	builder.WriteString(fmt.Sprintf("category: %s\n", candidate.Category))
	builder.WriteString(fmt.Sprintf("importance: %d\n", candidate.Importance))
	if candidate.SubjectActorID != "" {
		builder.WriteString(fmt.Sprintf("subject_actor_id: %s\n", candidate.SubjectActorID))
	}
	if candidate.SubjectActorName != "" {
		builder.WriteString(fmt.Sprintf("subject_actor_name: %s\n", candidate.SubjectActorName))
	}
	builder.WriteString("</candidate>\n\n")
	builder.WriteString("<existing_memories>\n")
	for _, match := range matches {
		builder.WriteString(fmt.Sprintf(
			"<memory id=%q similarity=%.3f kind=%q category=%q importance=%d>\n%s\n</memory>\n",
			match.Record.ID,
			match.Similarity,
			match.Record.Profile.Kind,
			match.Record.Category,
			memoryImportance(match.Record),
			match.Record.Content,
		))
	}
	builder.WriteString("</existing_memories>\n\n")
	builder.WriteString("Respond with one JSON object only.\n")
	builder.WriteString(`Format: {"action":"add|rewrite|supersede|noop","target_id":"...","content":"...","category":"...","importance":1,"subject_actor_id":"...","subject_actor_name":"...","absorbed_record_ids":["..."]}`)

	return builder.String()
}

func parseSynthesisDecision(text string) (synthesisDecision, error) {
	trimmed := strings.TrimSpace(stripMarkdownCodeFence(text))
	if trimmed == "" {
		return synthesisDecision{}, fmt.Errorf("empty synthesis decision")
	}

	var decision synthesisDecision
	if err := json.Unmarshal([]byte(trimmed), &decision); err != nil {
		extracted, extractErr := extractJSONObject(trimmed)
		if extractErr != nil {
			return synthesisDecision{}, fmt.Errorf("parse synthesis decision: %w", err)
		}
		if err := json.Unmarshal([]byte(extracted), &decision); err != nil {
			return synthesisDecision{}, fmt.Errorf("parse synthesis decision: %w", err)
		}
	}

	return decision, nil
}

func normalizeSynthesisDecision(
	decision synthesisDecision,
	candidate extractedMemory,
	matches []ai.LLMMemoryMatch,
	fallback synthesisDecision,
) synthesisDecision {
	decision.Action = synthesisAction(strings.TrimSpace(string(decision.Action)))
	decision.TargetID = strings.TrimSpace(decision.TargetID)
	decision.Content = strings.TrimSpace(decision.Content)
	decision.Category = strings.TrimSpace(decision.Category)
	decision.SubjectActorID = strings.TrimSpace(decision.SubjectActorID)
	decision.SubjectActorName = strings.TrimSpace(decision.SubjectActorName)

	switch decision.Action {
	case synthesisActionAdd, synthesisActionRewrite, synthesisActionSupersede, synthesisActionNoop:
	default:
		return fallback
	}

	if (decision.Action == synthesisActionRewrite || decision.Action == synthesisActionSupersede) &&
		!containsMemoryID(matches, decision.TargetID) {
		return fallback
	}
	if decision.Action == synthesisActionRewrite || decision.Action == synthesisActionAdd || decision.Action == synthesisActionSupersede {
		if decision.Content == "" {
			decision.Content = candidate.Content
		}
		if decision.Category == "" {
			decision.Category = candidate.Category
		}
		if decision.Importance < 1 || decision.Importance > 10 {
			decision.Importance = candidate.Importance
		}
		if decision.SubjectActorID == "" {
			decision.SubjectActorID = candidate.SubjectActorID
		}
		if decision.SubjectActorName == "" {
			decision.SubjectActorName = candidate.SubjectActorName
		}
	}
	if decision.Action == synthesisActionNoop {
		return decision
	}

	normalizedIDs := make([]string, 0, len(decision.AbsorbedRecordIDs))
	seen := make(map[string]struct{}, len(decision.AbsorbedRecordIDs))
	for _, recordID := range decision.AbsorbedRecordIDs {
		trimmed := strings.TrimSpace(recordID)
		if trimmed == "" || trimmed == decision.TargetID {
			continue
		}
		if !containsMemoryID(matches, trimmed) {
			continue
		}
		if _, exists := seen[trimmed]; exists {
			continue
		}
		seen[trimmed] = struct{}{}
		normalizedIDs = append(normalizedIDs, trimmed)
	}
	decision.AbsorbedRecordIDs = normalizedIDs

	return decision
}

func fallbackSynthesisDecision(
	candidate extractedMemory,
	matches []ai.LLMMemoryMatch,
	threshold float32,
) synthesisDecision {
	action, targetID := classifyCandidate(candidate, nil, matches, threshold)
	switch action {
	case dedupActionAdd:
		return synthesisDecision{
			Action:           synthesisActionAdd,
			Content:          candidate.Content,
			Category:         candidate.Category,
			Importance:       candidate.Importance,
			SubjectActorID:   candidate.SubjectActorID,
			SubjectActorName: candidate.SubjectActorName,
		}
	case dedupActionUpdate:
		return synthesisDecision{
			Action:           synthesisActionRewrite,
			TargetID:         targetID,
			Content:          candidate.Content,
			Category:         candidate.Category,
			Importance:       candidate.Importance,
			SubjectActorID:   candidate.SubjectActorID,
			SubjectActorName: candidate.SubjectActorName,
		}
	default:
		return synthesisDecision{Action: synthesisActionNoop}
	}
}

func containsMemoryID(matches []ai.LLMMemoryMatch, targetID string) bool {
	for _, match := range matches {
		if match.Record.ID == targetID {
			return true
		}
	}

	return false
}

func extractJSONObject(text string) (string, error) {
	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start < 0 || end <= start {
		return "", fmt.Errorf("json object not found")
	}

	return strings.TrimSpace(text[start : end+1]), nil
}
