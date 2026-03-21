package llmchat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"ex-otogi/pkg/otogi/ai"
	"ex-otogi/pkg/otogi/platform"
)

const (
	retrievalPlannerSystemPrompt = `You are a semantic memory retrieval planner. Produce a small set of search intents that will help retrieve the most relevant long-term memories for the current user message.`
	unitKindWeight               = 1.0
	synthesizedKindWeight        = 1.15
	currentActorWeight           = 1.15
	relatedActorWeight           = 1.05
	linkedBoostWeight            = 1.1
	keywordOverlapBonus          = 0.05
	maxKeywordBonusHits          = 4
)

type retrievalPlan struct {
	Queries    []string `json:"queries"`
	TimeFilter string   `json:"time_filter"`
	Depth      string   `json:"depth"`
}

func (m *Module) retrieveSemanticMemories(
	ctx context.Context,
	event *platform.Event,
	agent Agent,
	prompt string,
) (string, error) {
	policy := resolveSemanticMemoryPolicy(agent.SemanticMemory)
	if policy == nil || !policy.Enabled {
		return "", nil
	}
	if event == nil {
		return "", nil
	}
	if strings.TrimSpace(prompt) == "" {
		return "", nil
	}
	if m == nil || m.llmMemory == nil {
		return "", nil
	}

	embeddingProvider, usable, err := m.resolveSemanticMemoryEmbeddingProvider(agent)
	if err != nil {
		return "", fmt.Errorf("retrieve semantic memories resolve embedding provider: %w", err)
	}
	if !usable {
		return "", nil
	}

	settings := resolveNaturalMemorySettings(m.cfg.NaturalMemory)
	scope := semanticMemoryScope(event)
	m.debugSemanticMemoryRetrieve(ctx, scope, prompt)

	replyRootSummary := m.replyRootSummary(ctx, event)
	plan, err := m.buildSemanticMemoryPlan(ctx, prompt, replyRootSummary, settings)
	if err != nil {
		return "", fmt.Errorf("retrieve semantic memories build queries: %w", err)
	}
	m.debugSemanticMemoryPlan(ctx, plan, settings.RetrievalPlanningEnabled)
	if len(plan.Queries) == 0 {
		return "", nil
	}

	matches, err := m.searchSemanticMemoryQueries(ctx, scope, plan.Queries, embeddingProvider, policy, plan.Depth)
	if err != nil {
		return "", fmt.Errorf("retrieve semantic memories search: %w", err)
	}
	searchLimit := maxSemanticMemorySearchLimit(policy.MaxRetrievedMemories, len(plan.Queries), plan.Depth)
	m.debugSemanticMemorySearch(ctx, len(matches), searchLimit, plan.Depth)
	if plan.TimeFilter != "" {
		preFilterCount := len(matches)
		matches = filterMatchesByTime(matches, plan.TimeFilter, m.now())
		m.debugSemanticMemoryTimeFilter(ctx, preFilterCount, len(matches), plan.TimeFilter)
	}
	if len(matches) == 0 {
		return "", nil
	}

	relatedActors := m.replyChainActors(ctx, event)
	queryTerms := extractQueryTerms(plan.Queries)
	ranked := rankSemanticMemoryMatches(matches, settings.DecayFactor, m.now(), event.Actor, relatedActors, queryTerms)
	selected := selectSemanticMemoryMatches(ranked, policy.MaxMemoryRunes)
	m.debugSemanticMemoryRank(ctx, len(ranked), len(selected), len(queryTerms))
	if len(selected) == 0 {
		return "", nil
	}
	if err := m.reinforceSemanticMemoryMatches(ctx, selected); err != nil && m.logger != nil {
		m.logger.WarnContext(ctx, "llmchat reinforce semantic memories", "error", err)
	}

	serialized := renderSemanticMemoryDocument(selected)
	var backgroundCount, recalledCount int
	for _, match := range selected {
		if isBackgroundMemory(match.Record) {
			backgroundCount++
		} else {
			recalledCount++
		}
	}
	m.debugSemanticMemoryRetrieveResult(ctx, scope, len(selected), len(serialized), backgroundCount, recalledCount)

	return serialized, nil
}

func (m *Module) resolveSemanticMemoryEmbeddingProvider(agent Agent) (ai.EmbeddingProvider, bool, error) {
	policy := resolveSemanticMemoryPolicy(agent.SemanticMemory)
	if policy == nil || !policy.Enabled {
		return nil, false, nil
	}
	if strings.TrimSpace(agent.EmbeddingProvider) == "" {
		return nil, false, nil
	}
	if m.embeddingRegistry == nil {
		return nil, false, nil
	}

	provider, err := m.embeddingRegistry.Resolve(agent.EmbeddingProvider)
	if err != nil {
		return nil, false, fmt.Errorf("embedding provider %s: %w", agent.EmbeddingProvider, err)
	}

	return provider, true, nil
}

func semanticMemoryScope(event *platform.Event) ai.LLMMemoryScope {
	if event == nil {
		return ai.LLMMemoryScope{}
	}

	return ai.LLMMemoryScope{
		TenantID:       event.TenantID,
		Platform:       string(event.Source.Platform),
		ConversationID: event.Conversation.ID,
	}
}

func (m *Module) buildSemanticMemoryPlan(
	ctx context.Context,
	prompt string,
	replyRootSummary string,
	settings NaturalMemorySettings,
) (retrievalPlan, error) {
	if settings.RetrievalPlanningEnabled {
		if plan, err := m.planSemanticMemoryQueries(ctx, prompt, replyRootSummary, settings); err == nil && len(plan.Queries) > 0 {
			plan.Queries = dedupeQueries(plan.Queries)
			return plan, nil
		}
	}

	return retrievalPlan{
		Queries: heuristicSemanticMemoryQueries(prompt, replyRootSummary),
	}, nil
}

func (m *Module) planSemanticMemoryQueries(
	ctx context.Context,
	prompt string,
	replyRootSummary string,
	settings NaturalMemorySettings,
) (retrievalPlan, error) {
	if m == nil || m.providerRegistry == nil {
		return retrievalPlan{}, fmt.Errorf("provider registry unavailable")
	}
	if strings.TrimSpace(settings.ExtractionProvider) == "" || strings.TrimSpace(settings.ExtractionModel) == "" {
		return retrievalPlan{}, fmt.Errorf("natural memory planner is not configured")
	}

	provider, err := m.providerRegistry.Resolve(settings.ExtractionProvider)
	if err != nil {
		return retrievalPlan{}, fmt.Errorf("resolve planner provider %s: %w", settings.ExtractionProvider, err)
	}

	requestCtx := ctx
	cancel := func() {}
	if settings.RetrievalPlanningTimeout > 0 {
		requestCtx, cancel = context.WithTimeout(ctx, settings.RetrievalPlanningTimeout)
	}
	defer cancel()

	stream, err := provider.GenerateStream(requestCtx, ai.LLMGenerateRequest{
		Model: settings.ExtractionModel,
		Messages: []ai.LLMMessage{
			{Role: ai.LLMMessageRoleSystem, Content: retrievalPlannerSystemPrompt},
			{Role: ai.LLMMessageRoleUser, Content: renderRetrievalPlanPrompt(prompt, replyRootSummary)},
		},
		Temperature: 0.1,
	})
	if err != nil {
		return retrievalPlan{}, fmt.Errorf("generate retrieval plan: %w", err)
	}

	responseText, err := collectStreamText(requestCtx, stream)
	closeErr := stream.Close()
	if err != nil {
		if closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close retrieval plan stream: %w", closeErr))
		}
		return retrievalPlan{}, err
	}
	if closeErr != nil {
		return retrievalPlan{}, fmt.Errorf("close retrieval plan stream: %w", closeErr)
	}

	plan, err := parseRetrievalPlanResponse(responseText)
	if err != nil {
		return retrievalPlan{}, err
	}

	return plan, nil
}

func renderRetrievalPlanPrompt(prompt string, replyRootSummary string) string {
	var builder strings.Builder

	builder.WriteString("Create up to three semantic search queries for long-term memory retrieval.\n\n")
	builder.WriteString("Rules:\n")
	builder.WriteString("- Keep each query short and specific\n")
	builder.WriteString("- Prefer explicit subjects, preferences, projects, plans, corrections, and notable experiences\n")
	builder.WriteString("- Include hidden context from the reply root when the current message is vague or follow-up-like\n")
	builder.WriteString("- Return at least one query\n\n")
	builder.WriteString("<current_message>\n")
	builder.WriteString(strings.TrimSpace(prompt))
	builder.WriteString("\n</current_message>\n\n")
	if strings.TrimSpace(replyRootSummary) != "" {
		builder.WriteString("<reply_root>\n")
		builder.WriteString(strings.TrimSpace(replyRootSummary))
		builder.WriteString("\n</reply_root>\n\n")
	}
	builder.WriteString("Respond with one JSON object:\n")
	builder.WriteString(`{"queries":["...","..."],"time_filter":"all|recent|last_week","depth":"few|normal|deep"}`)
	builder.WriteString("\n\n")
	builder.WriteString("time_filter: \"recent\" = last 24h, \"last_week\" = last 7 days, \"all\" = no filter (default).\n")
	builder.WriteString("depth: \"few\" = lightweight retrieval, \"normal\" = standard (default), \"deep\" = thorough retrieval.\n")

	return builder.String()
}

func parseRetrievalPlanResponse(text string) (retrievalPlan, error) {
	trimmed := strings.TrimSpace(stripMarkdownCodeFence(text))
	if trimmed == "" {
		return retrievalPlan{}, fmt.Errorf("empty retrieval plan")
	}

	var plan retrievalPlan
	if err := json.Unmarshal([]byte(trimmed), &plan); err != nil {
		extracted, extractErr := extractJSONObject(trimmed)
		if extractErr != nil {
			return retrievalPlan{}, fmt.Errorf("parse retrieval plan: %w", err)
		}
		if err := json.Unmarshal([]byte(extracted), &plan); err != nil {
			return retrievalPlan{}, fmt.Errorf("parse retrieval plan: %w", err)
		}
	}

	plan.Queries = dedupeQueries(plan.Queries)
	plan.TimeFilter = normalizeTimeFilter(strings.TrimSpace(plan.TimeFilter))
	plan.Depth = normalizeDepth(strings.TrimSpace(plan.Depth))

	return plan, nil
}

func heuristicSemanticMemoryQueries(prompt string, replyRootSummary string) []string {
	queries := []string{strings.TrimSpace(prompt)}
	if strings.TrimSpace(replyRootSummary) != "" && looksLikeFollowUpPrompt(prompt) {
		queries = append(queries, strings.TrimSpace(replyRootSummary)+"\n"+strings.TrimSpace(prompt))
	}

	return dedupeQueries(queries)
}

func looksLikeFollowUpPrompt(prompt string) bool {
	trimmed := strings.ToLower(strings.TrimSpace(prompt))
	if trimmed == "" {
		return false
	}
	if len(strings.Fields(trimmed)) <= 4 {
		return true
	}

	prefixes := []string{"and ", "also ", "what about", "that ", "it ", "they ", "he ", "she ", "those ", "them "}
	for _, prefix := range prefixes {
		if strings.HasPrefix(trimmed, prefix) {
			return true
		}
	}

	return false
}

func dedupeQueries(queries []string) []string {
	seen := make(map[string]struct{}, len(queries))
	deduped := make([]string, 0, len(queries))
	for _, query := range queries {
		trimmed := strings.TrimSpace(query)
		if trimmed == "" {
			continue
		}
		if _, exists := seen[trimmed]; exists {
			continue
		}
		seen[trimmed] = struct{}{}
		deduped = append(deduped, trimmed)
	}

	return deduped
}

func (m *Module) searchSemanticMemoryQueries(
	ctx context.Context,
	scope ai.LLMMemoryScope,
	queries []string,
	embeddingProvider ai.EmbeddingProvider,
	policy *SemanticMemoryPolicy,
	depth string,
) ([]ai.LLMMemoryMatch, error) {
	merged := make(map[string]ai.LLMMemoryMatch)
	limit := maxSemanticMemorySearchLimit(policy.MaxRetrievedMemories, len(queries), depth)

	for _, query := range queries {
		queryEmbedding, err := embedSingleText(ctx, embeddingProvider, query, ai.EmbeddingTaskTypeQuery)
		if err != nil {
			return nil, fmt.Errorf("embed semantic memory query %q: %w", query, err)
		}

		matches, err := m.llmMemory.Search(ctx, ai.LLMMemoryQuery{
			Scope:         scope,
			Embedding:     queryEmbedding,
			Limit:         limit,
			MinSimilarity: policy.MinMemorySimilarity,
		})
		if err != nil {
			return nil, fmt.Errorf("search semantic memories for query %q: %w", query, err)
		}
		for _, match := range matches {
			existing, exists := merged[match.Record.ID]
			if !exists || match.Similarity > existing.Similarity {
				merged[match.Record.ID] = match
			}
		}
	}

	result := make([]ai.LLMMemoryMatch, 0, len(merged))
	for _, match := range merged {
		result = append(result, match)
	}

	return result, nil
}

func rankSemanticMemoryMatches(
	matches []ai.LLMMemoryMatch,
	decayFactor float64,
	now time.Time,
	currentActor platform.Actor,
	relatedActors map[string]struct{},
	queryTerms []string,
) []ai.LLMMemoryMatch {
	type scoredMatch struct {
		match      ai.LLMMemoryMatch
		finalScore float64
	}

	matchIDs := make(map[string]struct{}, len(matches))
	for _, match := range matches {
		matchIDs[match.Record.ID] = struct{}{}
	}

	scored := make([]scoredMatch, 0, len(matches))
	for _, match := range matches {
		importanceWeight := 0.5 + (float64(semanticMemoryImportance(match.Record)) / 20.0)
		recencyWeight := math.Pow(decayFactor, now.Sub(semanticMemoryLastAccessed(match.Record)).Hours())
		finalScore := float64(match.Similarity) *
			importanceWeight *
			recencyWeight *
			semanticMemoryKindWeight(match.Record) *
			semanticMemoryActorWeight(match.Record, currentActor, relatedActors)

		scored = append(scored, scoredMatch{match: match, finalScore: finalScore})
	}

	// Boost records whose keywords overlap with query terms.
	if len(queryTerms) > 0 {
		queryTermSet := make(map[string]struct{}, len(queryTerms))
		for _, term := range queryTerms {
			queryTermSet[term] = struct{}{}
		}
		for index := range scored {
			overlap := 0
			for _, keyword := range scored[index].match.Record.Keywords {
				if _, exists := queryTermSet[strings.ToLower(keyword)]; exists {
					overlap++
				}
			}
			if overlap > 0 {
				hits := overlap
				if hits > maxKeywordBonusHits {
					hits = maxKeywordBonusHits
				}
				scored[index].finalScore *= 1.0 + keywordOverlapBonus*float64(hits)
			}
		}
	}

	// Boost records that are linked from other records in the match set.
	inboundLinkCounts := make(map[string]int, len(scored))
	for _, entry := range scored {
		for _, link := range entry.match.Record.Links {
			if _, exists := matchIDs[link.TargetID]; exists {
				inboundLinkCounts[link.TargetID]++
			}
		}
	}
	for index := range scored {
		if count := inboundLinkCounts[scored[index].match.Record.ID]; count > 0 {
			scored[index].finalScore *= linkedBoostWeight
		}
	}

	sort.Slice(scored, func(i, j int) bool {
		if scored[i].finalScore == scored[j].finalScore {
			return scored[i].match.Record.CreatedAt.After(scored[j].match.Record.CreatedAt)
		}
		return scored[i].finalScore > scored[j].finalScore
	})

	ranked := make([]ai.LLMMemoryMatch, len(scored))
	for index, entry := range scored {
		ranked[index] = entry.match
	}

	return ranked
}

func selectSemanticMemoryMatches(matches []ai.LLMMemoryMatch, maxRunes int) []ai.LLMMemoryMatch {
	if len(matches) == 0 {
		return nil
	}
	if maxRunes <= 0 {
		maxRunes = defaultMaxMemoryRunes
	}

	included := make([]ai.LLMMemoryMatch, 0, len(matches))
	for _, match := range matches {
		fitted, ok := fitSemanticMemoryMatch(included, match, maxRunes)
		if !ok {
			continue
		}
		included = append(included, fitted)
	}
	if len(included) == 0 {
		return nil
	}

	return included
}

func fitSemanticMemoryMatch(
	existing []ai.LLMMemoryMatch,
	match ai.LLMMemoryMatch,
	maxRunes int,
) (ai.LLMMemoryMatch, bool) {
	candidate := append(append([]ai.LLMMemoryMatch(nil), existing...), match)
	if runeCount(renderSemanticMemoryDocument(candidate)) <= maxRunes {
		return match, true
	}

	normalizedContent := strings.Join(strings.Fields(match.Record.Content), " ")
	if normalizedContent == "" {
		return ai.LLMMemoryMatch{}, false
	}

	best := ""
	low := 1
	high := len([]rune(normalizedContent))
	for low <= high {
		mid := (low + high) / 2
		trimmed := match
		trimmed.Record.Content = trimRunesWithEllipsis(normalizedContent, mid)
		testCandidate := append(append([]ai.LLMMemoryMatch(nil), existing...), trimmed)
		if runeCount(renderSemanticMemoryDocument(testCandidate)) <= maxRunes {
			best = trimmed.Record.Content
			low = mid + 1
			continue
		}

		high = mid - 1
	}
	if best == "" {
		return ai.LLMMemoryMatch{}, false
	}

	trimmed := match
	trimmed.Record.Content = best

	return trimmed, true
}

func renderSemanticMemoryDocument(matches []ai.LLMMemoryMatch) string {
	var background, recalled []ai.LLMMemoryMatch
	for _, match := range matches {
		if isBackgroundMemory(match.Record) {
			background = append(background, match)
		} else {
			recalled = append(recalled, match)
		}
	}

	var builder strings.Builder
	builder.WriteString(fmt.Sprintf("<semantic_memories count=\"%d\">\n", len(matches)))
	if len(background) > 0 {
		builder.WriteString(fmt.Sprintf("<tier role=\"background\" count=\"%d\">\n", len(background)))
		for _, match := range background {
			builder.WriteString(renderSemanticMemoryMatch(match))
			builder.WriteByte('\n')
		}
		builder.WriteString("</tier>\n")
	}
	if len(recalled) > 0 {
		builder.WriteString(fmt.Sprintf("<tier role=\"recalled\" count=\"%d\">\n", len(recalled)))
		for _, match := range recalled {
			builder.WriteString(renderSemanticMemoryMatch(match))
			builder.WriteByte('\n')
		}
		builder.WriteString("</tier>\n")
	}
	builder.WriteString("</semantic_memories>")
	return builder.String()
}

func isBackgroundMemory(record ai.LLMMemoryRecord) bool {
	switch record.Category {
	case "reflection", "theme":
		return true
	default:
		return false
	}
}

func renderSemanticMemoryMatch(match ai.LLMMemoryMatch) string {
	attributes := []string{
		fmt.Sprintf("id=\"%s\"", html.EscapeString(match.Record.ID)),
		fmt.Sprintf("category=\"%s\"", html.EscapeString(match.Record.Category)),
		fmt.Sprintf("kind=\"%s\"", html.EscapeString(string(semanticMemoryKind(match.Record)))),
		fmt.Sprintf("importance=\"%d\"", semanticMemoryImportance(match.Record)),
	}
	if match.Record.Profile.SubjectActor != nil && strings.TrimSpace(match.Record.Profile.SubjectActor.Name) != "" {
		attributes = append(attributes, fmt.Sprintf(
			"subject_actor=\"%s\"",
			html.EscapeString(match.Record.Profile.SubjectActor.Name),
		))
	}
	if match.Record.Profile.SourceActor != nil && strings.TrimSpace(match.Record.Profile.SourceActor.Name) != "" {
		attributes = append(attributes, fmt.Sprintf(
			"source_actor=\"%s\"",
			html.EscapeString(match.Record.Profile.SourceActor.Name),
		))
	}

	return fmt.Sprintf(
		"<memory %s>\n%s\n</memory>",
		strings.Join(attributes, " "),
		html.EscapeString(match.Record.Content),
	)
}

func (m *Module) reinforceSemanticMemoryMatches(ctx context.Context, matches []ai.LLMMemoryMatch) error {
	if m == nil || m.llmMemory == nil || len(matches) == 0 {
		return nil
	}

	var errs []error
	now := m.now()
	for _, match := range matches {
		profile := match.Record.Profile
		if profile.Kind == "" {
			profile.Kind = semanticMemoryKind(match.Record)
		}
		profile.LastAccessedAt = now
		profile.AccessCount++

		if _, err := m.llmMemory.Update(ctx, ai.LLMMemoryUpdate{
			ID:        match.Record.ID,
			Content:   match.Record.Content,
			Category:  match.Record.Category,
			Embedding: append([]float32(nil), match.Record.Embedding...),
			Profile:   profile,
			Metadata:  semanticMemoryMetadata(match.Record.Metadata, profile),
			Keywords:  append([]string(nil), match.Record.Keywords...),
			Tags:      append([]string(nil), match.Record.Tags...),
			Links:     append([]ai.LLMMemoryLink(nil), match.Record.Links...),
		}); err != nil {
			errs = append(errs, fmt.Errorf("reinforce semantic memory %s: %w", match.Record.ID, err))
		}
	}

	return errors.Join(errs...)
}

func (m *Module) replyRootSummary(ctx context.Context, event *platform.Event) string {
	if m == nil || m.memory == nil || event == nil {
		return ""
	}

	chain, err := m.memory.GetReplyChain(ctx, event)
	if err != nil || len(chain) == 0 {
		return ""
	}

	root := strings.TrimSpace(chain[0].Article.Text)
	if root == "" {
		return ""
	}

	return trimRunesWithEllipsis(strings.Join(strings.Fields(root), " "), 240)
}

func (m *Module) replyChainActors(ctx context.Context, event *platform.Event) map[string]struct{} {
	actors := make(map[string]struct{})
	if m == nil || m.memory == nil || event == nil {
		return actors
	}

	chain, err := m.memory.GetReplyChain(ctx, event)
	if err != nil {
		return actors
	}
	for _, entry := range chain {
		if id := strings.TrimSpace(entry.Actor.ID); id != "" {
			actors[id] = struct{}{}
		}
		if name := strings.ToLower(strings.TrimSpace(entry.Actor.DisplayName)); name != "" {
			actors[name] = struct{}{}
		}
		if name := strings.ToLower(strings.TrimSpace(entry.Actor.Username)); name != "" {
			actors[name] = struct{}{}
		}
	}

	return actors
}

func semanticMemoryImportance(record ai.LLMMemoryRecord) int {
	if record.Profile.Importance > 0 {
		return record.Profile.Importance
	}
	if raw := strings.TrimSpace(record.Metadata[ai.LLMMemoryMetadataImportance]); raw != "" {
		if value, err := strconv.Atoi(raw); err == nil {
			return value
		}
	}

	return 5
}

func semanticMemoryLastAccessed(record ai.LLMMemoryRecord) time.Time {
	if !record.Profile.LastAccessedAt.IsZero() {
		return record.Profile.LastAccessedAt.UTC()
	}
	if raw := strings.TrimSpace(record.Metadata[ai.LLMMemoryMetadataLastAccessed]); raw != "" {
		if parsed, err := time.Parse(time.RFC3339, raw); err == nil {
			return parsed.UTC()
		}
	}

	return record.CreatedAt.UTC()
}

func semanticMemoryKind(record ai.LLMMemoryRecord) ai.LLMMemoryKind {
	if record.Profile.Kind != "" {
		return record.Profile.Kind
	}

	return ai.LLMMemoryKindUnit
}

func semanticMemoryKindWeight(record ai.LLMMemoryRecord) float64 {
	switch semanticMemoryKind(record) {
	case ai.LLMMemoryKindSynthesized:
		return synthesizedKindWeight
	default:
		return unitKindWeight
	}
}

func semanticMemoryActorWeight(
	record ai.LLMMemoryRecord,
	currentActor platform.Actor,
	relatedActors map[string]struct{},
) float64 {
	if semanticMemoryActorMatches(record.Profile.SubjectActor, currentActor) ||
		semanticMemoryActorMatches(record.Profile.SourceActor, currentActor) {
		return currentActorWeight
	}
	if semanticMemoryActorInSet(record.Profile.SubjectActor, relatedActors) ||
		semanticMemoryActorInSet(record.Profile.SourceActor, relatedActors) {
		return relatedActorWeight
	}

	return 1.0
}

func semanticMemoryActorMatches(ref *ai.LLMMemoryActorRef, actor platform.Actor) bool {
	if ref == nil {
		return false
	}
	if ref.ID != "" && ref.ID == strings.TrimSpace(actor.ID) {
		return true
	}
	names := []string{
		strings.ToLower(strings.TrimSpace(ref.Name)),
	}
	candidates := []string{
		strings.ToLower(strings.TrimSpace(actor.DisplayName)),
		strings.ToLower(strings.TrimSpace(actor.Username)),
	}
	for _, name := range names {
		if name == "" {
			continue
		}
		for _, candidate := range candidates {
			if candidate != "" && name == candidate {
				return true
			}
		}
	}

	return false
}

func semanticMemoryActorInSet(ref *ai.LLMMemoryActorRef, values map[string]struct{}) bool {
	if ref == nil {
		return false
	}
	if ref.ID != "" {
		if _, exists := values[ref.ID]; exists {
			return true
		}
	}
	if ref.Name != "" {
		if _, exists := values[strings.ToLower(strings.TrimSpace(ref.Name))]; exists {
			return true
		}
	}

	return false
}

func semanticMemoryMetadata(existing map[string]string, profile ai.LLMMemoryProfile) map[string]string {
	metadata := cloneStringMap(existing)
	if metadata == nil {
		metadata = make(map[string]string)
	}
	metadata[ai.LLMMemoryMetadataImportance] = fmt.Sprintf("%d", semanticMemoryImportance(ai.LLMMemoryRecord{Profile: profile}))
	metadata[ai.LLMMemoryMetadataAccessCount] = fmt.Sprintf("%d", profile.AccessCount)
	metadata[ai.LLMMemoryMetadataLastAccessed] = profile.LastAccessedAt.UTC().Format(time.RFC3339)
	if strings.TrimSpace(profile.Source) != "" {
		metadata[ai.LLMMemoryMetadataSource] = strings.TrimSpace(profile.Source)
	}
	if strings.TrimSpace(profile.SourceArticleID) != "" {
		metadata[ai.LLMMemoryMetadataSourceArticleID] = strings.TrimSpace(profile.SourceArticleID)
	}
	if profile.SourceActor != nil {
		if profile.SourceActor.ID != "" {
			metadata[ai.LLMMemoryMetadataSourceActorID] = profile.SourceActor.ID
		}
		if profile.SourceActor.Name != "" {
			metadata[ai.LLMMemoryMetadataSourceActorName] = profile.SourceActor.Name
		}
		metadata[ai.LLMMemoryMetadataSourceActorIsBot] = fmt.Sprintf("%t", profile.SourceActor.IsBot)
	}
	if profile.SubjectActor != nil {
		if profile.SubjectActor.ID != "" {
			metadata[ai.LLMMemoryMetadataSubjectActorID] = profile.SubjectActor.ID
		}
		if profile.SubjectActor.Name != "" {
			metadata[ai.LLMMemoryMetadataSubjectActorName] = profile.SubjectActor.Name
		}
		metadata[ai.LLMMemoryMetadataSubjectActorIsBot] = fmt.Sprintf("%t", profile.SubjectActor.IsBot)
	}
	if len(profile.EvidenceRecordIDs) > 0 {
		metadata[ai.LLMMemoryMetadataSourceRecordIDs] = strings.Join(profile.EvidenceRecordIDs, ",")
	}

	return metadata
}

func maxSemanticMemorySearchLimit(base int, queryCount int, depth string) int {
	if base <= 0 {
		base = defaultMaxRetrievedMemories
	}

	switch depth {
	case "few":
		return base
	case "deep":
		scaled := base * 3
		if queryCount > 1 {
			return maxInt(scaled, base+queryCount)
		}
		return maxInt(scaled, base)
	default:
		// "normal" or empty — preserve existing behavior (2×).
		scaled := base * 2
		if queryCount > 1 {
			return maxInt(scaled, base+queryCount)
		}
		return maxInt(scaled, base)
	}
}

func normalizeTimeFilter(raw string) string {
	switch strings.ToLower(raw) {
	case "recent":
		return "recent"
	case "last_week":
		return "last_week"
	default:
		return ""
	}
}

func normalizeDepth(raw string) string {
	switch strings.ToLower(raw) {
	case "few":
		return "few"
	case "deep":
		return "deep"
	default:
		return ""
	}
}

func filterMatchesByTime(matches []ai.LLMMemoryMatch, filter string, now time.Time) []ai.LLMMemoryMatch {
	var cutoff time.Time
	switch filter {
	case "recent":
		cutoff = now.Add(-24 * time.Hour)
	case "last_week":
		cutoff = now.Add(-7 * 24 * time.Hour)
	default:
		return matches
	}

	filtered := make([]ai.LLMMemoryMatch, 0, len(matches))
	for _, match := range matches {
		if !match.Record.CreatedAt.Before(cutoff) {
			filtered = append(filtered, match)
		}
	}

	return filtered
}

func extractQueryTerms(queries []string) []string {
	seen := make(map[string]struct{})
	var terms []string
	for _, query := range queries {
		for _, word := range strings.Fields(strings.ToLower(query)) {
			cleaned := strings.Trim(word, ".,;:!?\"'()[]{}")
			if cleaned == "" || len(cleaned) < 3 {
				continue
			}
			if _, exists := seen[cleaned]; exists {
				continue
			}
			seen[cleaned] = struct{}{}
			terms = append(terms, cleaned)
		}
	}

	return terms
}

func maxInt(left int, right int) int {
	if left > right {
		return left
	}

	return right
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

func extractJSONObject(text string) (string, error) {
	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start < 0 || end <= start {
		return "", fmt.Errorf("json object not found")
	}

	return strings.TrimSpace(text[start : end+1]), nil
}
