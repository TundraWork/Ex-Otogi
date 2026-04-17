package memory

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
	panel "ex-otogi/pkg/otogi/management"
)

const (
	// retrievalPlannerSystemPrompt instructs the planner LLM to produce a small
	// set of search intents for one user message.
	retrievalPlannerSystemPrompt = `You are a semantic memory retrieval planner. Produce a small set of search intents that will help retrieve the most relevant long-term memories for the current user message.`
	// currentActorWeight boosts records whose subject or source actor matches
	// the current speaker.
	currentActorWeight = 1.15
	// relatedActorWeight boosts records whose subject or source actor matches
	// another participant in the reply chain.
	relatedActorWeight = 1.05
	// keywordOverlapBonus scales the multiplicative boost applied per overlapping
	// keyword hit.
	keywordOverlapBonus = 0.05
	// maxKeywordBonusHits caps how many overlapping keywords can contribute to
	// the keyword overlap bonus.
	maxKeywordBonusHits = 4

	// defaultMaxRetrievedMemories is the fallback cap for returned memories.
	defaultMaxRetrievedMemories = 5
	// defaultMinMemorySimilarity is the fallback minimum similarity score used
	// when filtering search hits.
	defaultMinMemorySimilarity = 0.3
	// defaultMaxMemoryRunes is the fallback rendered-content rune cap.
	defaultMaxMemoryRunes = 2000
)

// retrievalPlan captures one planner-produced search plan.
type retrievalPlan struct {
	Queries    []string `json:"queries"`
	TimeFilter string   `json:"time_filter"`
	Depth      string   `json:"depth"`
}

// Retrieve plans, searches, ranks, and renders semantic memories for one
// request.
//
// Retrieve returns an empty result without error when the module cannot
// serve the request — specifically when the module is disabled, the
// backing semantic store or embedding registry is not wired, the request
// omits the prompt or embedding provider name, the planner yields no
// queries, or ranking and filtering produce no selected matches. Failures
// from configured-but-unavailable dependencies (resolving the embedding
// provider, building the query plan, or searching the store) are returned
// as errors so callers can distinguish misconfiguration from quiet misses.
func (m *Module) Retrieve(
	ctx context.Context,
	req ai.SemanticRetrievalRequest,
) (ai.SemanticRetrievalResult, error) {
	if m == nil || m.semanticStore == nil || !m.cfg.Enabled {
		return ai.SemanticRetrievalResult{}, nil
	}
	if strings.TrimSpace(req.Prompt) == "" {
		return ai.SemanticRetrievalResult{}, nil
	}
	if strings.TrimSpace(req.EmbeddingProvider) == "" {
		return ai.SemanticRetrievalResult{}, nil
	}
	if m.embeddingRegistry == nil {
		return ai.SemanticRetrievalResult{}, nil
	}

	embeddingProvider, err := m.embeddingRegistry.Resolve(req.EmbeddingProvider)
	if err != nil {
		return ai.SemanticRetrievalResult{}, fmt.Errorf(
			"semantic retrieve resolve embedding provider %s: %w",
			req.EmbeddingProvider,
			err,
		)
	}

	policy := resolveRetrievalPolicy(req.Policy)
	scope := req.Scope
	prompt := strings.TrimSpace(req.Prompt)

	relatedActorSet := make(map[string]struct{}, len(req.RelatedActors)*2)
	for _, actor := range req.RelatedActors {
		if id := strings.TrimSpace(actor.ID); id != "" {
			relatedActorSet[id] = struct{}{}
		}
		if name := strings.ToLower(strings.TrimSpace(actor.Name)); name != "" {
			relatedActorSet[name] = struct{}{}
		}
	}

	retrieveStart := m.now()
	m.debugSemanticMemoryRetrieve(ctx, scope, prompt)
	m.emitManagementEvent(
		ctx,
		&scope,
		"memory.retrieve.started",
		"started semantic memory retrieval",
		panel.TruncateDescription(prompt),
		panel.MemoryRetrieveStartedPayload{
			Provider:   req.EmbeddingProvider,
			QueryCount: 1,
		},
	)

	plan, err := m.buildSemanticMemoryPlan(ctx, prompt, req.ReplyRootSummary)
	if err != nil {
		return ai.SemanticRetrievalResult{}, fmt.Errorf("semantic retrieve build queries: %w", err)
	}
	m.debugSemanticMemoryPlan(ctx, plan, m.cfg.RetrievalPlanningEnabled)
	m.emitManagementEvent(
		ctx,
		&scope,
		"memory.retrieve.planned",
		"planned semantic memory retrieval",
		panel.TruncateDescription(strings.Join(plan.Queries, ", ")),
		panel.MemoryRetrievePlanPayload{
			Queries:     append([]string(nil), plan.Queries...),
			TimeFilter:  plan.TimeFilter,
			Depth:       plan.Depth,
			PlannerUsed: m.cfg.RetrievalPlanningEnabled,
		},
	)
	if len(plan.Queries) == 0 {
		return ai.SemanticRetrievalResult{}, nil
	}

	matches, err := m.searchSemanticMemoryQueries(ctx, scope, plan.Queries, embeddingProvider, policy, plan.Depth)
	if err != nil {
		return ai.SemanticRetrievalResult{}, fmt.Errorf("semantic retrieve search: %w", err)
	}

	searchLimit := maxSemanticMemorySearchLimit(policy.MaxRetrievedMemories, len(plan.Queries), plan.Depth)
	m.debugSemanticMemorySearch(ctx, len(matches), searchLimit, plan.Depth)
	returned := len(matches)
	if returned > searchLimit {
		returned = searchLimit
	}
	m.emitManagementEvent(
		ctx,
		&scope,
		"memory.retrieve.searched",
		"searched semantic memory candidates",
		panel.TruncateDescription(fmt.Sprintf("%d candidates, %d returned", len(matches), returned)),
		panel.MemoryRetrieveSearchedPayload{
			CandidateCount: len(matches),
			ReturnedCount:  returned,
		},
	)

	if plan.TimeFilter != "" {
		preFilterCount := len(matches)
		matches = filterMatchesByTime(matches, plan.TimeFilter, m.now())
		m.debugSemanticMemoryTimeFilter(ctx, preFilterCount, len(matches), plan.TimeFilter)
	}
	if len(matches) == 0 {
		return ai.SemanticRetrievalResult{}, nil
	}

	queryTerms := extractQueryTerms(plan.Queries)
	ranked := rankSemanticMemoryMatches(matches, m.cfg.DecayFactor, m.now(), req.CurrentActor, relatedActorSet, queryTerms)
	selected := selectSemanticMemoryMatches(ranked, policy.MaxMemoryRunes)
	m.debugSemanticMemoryRank(ctx, len(ranked), len(selected), len(queryTerms))
	if len(selected) == 0 {
		return ai.SemanticRetrievalResult{}, nil
	}
	if err := m.reinforceSemanticMemoryMatches(ctx, selected); err != nil && m.logger != nil {
		m.logger.WarnContext(ctx, "memory reinforce semantic memories", "error", err)
	}

	serialized := renderSemanticMemoryDocument(selected)
	m.debugSemanticMemoryRetrieveResult(ctx, scope, len(selected), len(serialized), 0, len(selected))
	m.emitManagementEvent(
		ctx,
		&scope,
		"memory.retrieve.completed",
		"completed semantic memory retrieval",
		panel.TruncateDescription(serialized),
		panel.MemoryRetrieveCompletedPayload{
			ResultCount: len(selected),
			ElapsedMS:   m.now().Sub(retrieveStart).Milliseconds(),
		},
	)

	return ai.SemanticRetrievalResult{
		Content:    serialized,
		MatchCount: len(selected),
	}, nil
}

// Available reports whether the module can serve retrieval requests for the
// named embedding provider.
//
// Callers use this to decide whether building heavier retrieval context is
// worthwhile before issuing the request.
func (m *Module) Available(embeddingProvider string) bool {
	if m == nil || !m.cfg.Enabled {
		return false
	}
	if m.semanticStore == nil || m.embeddingRegistry == nil {
		return false
	}
	if strings.TrimSpace(embeddingProvider) == "" {
		return false
	}
	if _, err := m.embeddingRegistry.Resolve(embeddingProvider); err != nil {
		return false
	}

	return true
}

// resolveRetrievalPolicy fills zero-valued policy fields with package defaults.
func resolveRetrievalPolicy(policy ai.SemanticRetrievalPolicy) ai.SemanticRetrievalPolicy {
	resolved := policy
	if resolved.MaxRetrievedMemories <= 0 {
		resolved.MaxRetrievedMemories = defaultMaxRetrievedMemories
	}
	if resolved.MinSimilarity <= 0 {
		resolved.MinSimilarity = defaultMinMemorySimilarity
	}
	if resolved.MaxMemoryRunes <= 0 {
		resolved.MaxMemoryRunes = defaultMaxMemoryRunes
	}

	return resolved
}

// buildSemanticMemoryPlan produces the search plan for one retrieval request,
// preferring the planner LLM when enabled and falling back to a heuristic
// plan otherwise.
func (m *Module) buildSemanticMemoryPlan(
	ctx context.Context,
	prompt string,
	replyRootSummary string,
) (retrievalPlan, error) {
	if m.cfg.RetrievalPlanningEnabled {
		if plan, err := m.planSemanticMemoryQueries(ctx, prompt, replyRootSummary); err == nil && len(plan.Queries) > 0 {
			plan.Queries = dedupeQueries(plan.Queries)
			return plan, nil
		}
	}

	return retrievalPlan{
		Queries: heuristicSemanticMemoryQueries(prompt, replyRootSummary),
	}, nil
}

// planSemanticMemoryQueries asks the planner LLM for a structured retrieval
// plan.
func (m *Module) planSemanticMemoryQueries(
	ctx context.Context,
	prompt string,
	replyRootSummary string,
) (retrievalPlan, error) {
	if m == nil || m.providerRegistry == nil {
		return retrievalPlan{}, fmt.Errorf("provider registry unavailable")
	}
	if strings.TrimSpace(m.cfg.ExtractionProvider) == "" || strings.TrimSpace(m.cfg.ExtractionModel) == "" {
		return retrievalPlan{}, fmt.Errorf("semantic memory planner is not configured")
	}

	provider, err := m.providerRegistry.Resolve(m.cfg.ExtractionProvider)
	if err != nil {
		return retrievalPlan{}, fmt.Errorf("resolve planner provider %s: %w", m.cfg.ExtractionProvider, err)
	}

	requestCtx := ctx
	cancel := func() {}
	if m.cfg.RetrievalPlanningTimeout > 0 {
		requestCtx, cancel = context.WithTimeout(ctx, m.cfg.RetrievalPlanningTimeout)
	}
	defer cancel()

	req := ai.LLMGenerateRequest{
		Model: m.cfg.ExtractionModel,
		Messages: []ai.LLMMessage{
			{Role: ai.LLMMessageRoleSystem, Content: retrievalPlannerSystemPrompt},
			{Role: ai.LLMMessageRoleUser, Content: renderRetrievalPlanPrompt(prompt, replyRootSummary)},
		},
		Temperature: 0.1,
	}
	responseText, err := retryLLMOperation(
		requestCtx,
		m.sleep,
		m.logger,
		"semantic memory retrieval planner",
		provider,
		func() (string, error) {
			stream, err := provider.GenerateStream(requestCtx, req)
			if err != nil {
				return "", fmt.Errorf("generate retrieval plan: %w", err)
			}

			responseText, err := collectStreamText(requestCtx, stream)
			closeErr := stream.Close()
			if err != nil {
				if closeErr != nil {
					err = errors.Join(err, fmt.Errorf("close retrieval plan stream: %w", closeErr))
				}
				return "", err
			}
			if closeErr != nil {
				return "", fmt.Errorf("close retrieval plan stream: %w", closeErr)
			}

			return responseText, nil
		},
	)
	if err != nil {
		return retrievalPlan{}, err
	}

	plan, err := parseRetrievalPlanResponse(responseText)
	if err != nil {
		return retrievalPlan{}, err
	}

	return plan, nil
}

// renderRetrievalPlanPrompt renders the planner user prompt for one request.
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

// parseRetrievalPlanResponse decodes one planner LLM response into a plan.
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

// heuristicSemanticMemoryQueries returns the fallback query list when the
// planner is disabled or unavailable.
func heuristicSemanticMemoryQueries(prompt string, replyRootSummary string) []string {
	queries := []string{strings.TrimSpace(prompt)}
	if strings.TrimSpace(replyRootSummary) != "" && looksLikeFollowUpPrompt(prompt) {
		queries = append(queries, strings.TrimSpace(replyRootSummary)+"\n"+strings.TrimSpace(prompt))
	}

	return dedupeQueries(queries)
}

// looksLikeFollowUpPrompt reports whether the prompt looks like a short
// follow-up that benefits from reply-root context.
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

// dedupeQueries removes blank and duplicate query entries, preserving order.
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

// searchSemanticMemoryQueries issues one search per plan query and merges the
// hits, keeping the best similarity score per record.
func (m *Module) searchSemanticMemoryQueries(
	ctx context.Context,
	scope ai.SemanticScope,
	queries []string,
	embeddingProvider ai.EmbeddingProvider,
	policy ai.SemanticRetrievalPolicy,
	depth string,
) ([]ai.SemanticMatch, error) {
	merged := make(map[string]ai.SemanticMatch)
	limit := maxSemanticMemorySearchLimit(policy.MaxRetrievedMemories, len(queries), depth)

	for _, query := range queries {
		queryEmbedding, err := embedSingleText(ctx, embeddingProvider, query, ai.EmbeddingTaskTypeQuery)
		if err != nil {
			return nil, fmt.Errorf("embed semantic memory query %q: %w", query, err)
		}

		matches, err := m.semanticStore.Search(ctx, ai.SemanticQuery{
			Scope:         scope,
			Embedding:     queryEmbedding,
			Limit:         limit,
			MinSimilarity: policy.MinSimilarity,
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

	result := make([]ai.SemanticMatch, 0, len(merged))
	for _, match := range merged {
		result = append(result, match)
	}

	return result, nil
}

// rankSemanticMemoryMatches sorts matches by a composite score combining
// similarity, importance, recency decay, actor weighting, and keyword overlap.
func rankSemanticMemoryMatches(
	matches []ai.SemanticMatch,
	decayFactor float64,
	now time.Time,
	currentActor ai.SemanticActorRef,
	relatedActors map[string]struct{},
	queryTerms []string,
) []ai.SemanticMatch {
	type scoredMatch struct {
		match      ai.SemanticMatch
		finalScore float64
	}

	scored := make([]scoredMatch, 0, len(matches))
	for _, match := range matches {
		importanceWeight := 0.5 + (float64(semanticMemoryImportance(match.Record)) / 20.0)
		recencyWeight := math.Pow(decayFactor, now.Sub(semanticMemoryLastAccessed(match.Record)).Hours())
		finalScore := float64(match.Similarity) *
			importanceWeight *
			recencyWeight *
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

	sort.Slice(scored, func(i, j int) bool {
		if scored[i].finalScore == scored[j].finalScore {
			return scored[i].match.Record.CreatedAt.After(scored[j].match.Record.CreatedAt)
		}
		return scored[i].finalScore > scored[j].finalScore
	})

	ranked := make([]ai.SemanticMatch, len(scored))
	for index, entry := range scored {
		ranked[index] = entry.match
	}

	return ranked
}

// selectSemanticMemoryMatches returns the ranked matches that fit within
// maxRunes when serialized, trimming individual contents when necessary.
func selectSemanticMemoryMatches(matches []ai.SemanticMatch, maxRunes int) []ai.SemanticMatch {
	if len(matches) == 0 {
		return nil
	}
	if maxRunes <= 0 {
		maxRunes = defaultMaxMemoryRunes
	}

	included := make([]ai.SemanticMatch, 0, len(matches))
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

// fitSemanticMemoryMatch returns a possibly trimmed match that, together with
// existing matches, renders within maxRunes. It returns ok=false when no trim
// makes the match fit.
func fitSemanticMemoryMatch(
	existing []ai.SemanticMatch,
	match ai.SemanticMatch,
	maxRunes int,
) (ai.SemanticMatch, bool) {
	candidate := append(append([]ai.SemanticMatch(nil), existing...), match)
	if runeCount(renderSemanticMemoryDocument(candidate)) <= maxRunes {
		return match, true
	}

	normalizedContent := strings.Join(strings.Fields(match.Record.Content), " ")
	if normalizedContent == "" {
		return ai.SemanticMatch{}, false
	}

	best := ""
	low := 1
	high := len([]rune(normalizedContent))
	for low <= high {
		mid := (low + high) / 2
		trimmed := match
		trimmed.Record.Content = trimRunesWithEllipsis(normalizedContent, mid)
		testCandidate := append(append([]ai.SemanticMatch(nil), existing...), trimmed)
		if runeCount(renderSemanticMemoryDocument(testCandidate)) <= maxRunes {
			best = trimmed.Record.Content
			low = mid + 1
			continue
		}

		high = mid - 1
	}
	if best == "" {
		return ai.SemanticMatch{}, false
	}

	trimmed := match
	trimmed.Record.Content = best

	return trimmed, true
}

// renderSemanticMemoryDocument renders one <semantic_memories> XML document
// from the selected matches.
func renderSemanticMemoryDocument(matches []ai.SemanticMatch) string {
	var builder strings.Builder
	builder.WriteString(fmt.Sprintf("<semantic_memories count=\"%d\">\n", len(matches)))
	for _, match := range matches {
		builder.WriteString(renderSemanticMemoryMatch(match))
		builder.WriteByte('\n')
	}
	builder.WriteString("</semantic_memories>")
	return builder.String()
}

// renderSemanticMemoryMatch renders one <memory> entry for one ranked match.
func renderSemanticMemoryMatch(match ai.SemanticMatch) string {
	attributes := []string{
		fmt.Sprintf("id=\"%s\"", html.EscapeString(match.Record.ID)),
		fmt.Sprintf("category=\"%s\"", html.EscapeString(match.Record.Category)),
		fmt.Sprintf("kind=\"%s\"", html.EscapeString(string(match.Record.Profile.Kind))),
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

// reinforceSemanticMemoryMatches bumps the last-accessed and access-count
// fields for each selected match so that successful retrievals count as
// reinforcement.
func (m *Module) reinforceSemanticMemoryMatches(ctx context.Context, matches []ai.SemanticMatch) error {
	if m == nil || m.semanticStore == nil || len(matches) == 0 {
		return nil
	}

	var errs []error
	now := m.now()
	for _, match := range matches {
		profile := match.Record.Profile
		if profile.Kind == "" {
			profile.Kind = match.Record.Profile.Kind
		}
		profile.LastAccessedAt = now
		profile.AccessCount++

		if _, err := m.semanticStore.Update(ctx, ai.SemanticUpdate{
			ID:        match.Record.ID,
			Content:   match.Record.Content,
			Category:  match.Record.Category,
			Embedding: append([]float32(nil), match.Record.Embedding...),
			Profile:   profile,
			Metadata:  semanticMemoryMetadata(match.Record.Metadata, profile),
			Keywords:  append([]string(nil), match.Record.Keywords...),
			Tags:      append([]string(nil), match.Record.Tags...),
			Links:     append([]ai.SemanticLink(nil), match.Record.Links...),
		}); err != nil {
			errs = append(errs, fmt.Errorf("reinforce semantic memory %s: %w", match.Record.ID, err))
		}
	}

	return errors.Join(errs...)
}

// semanticMemoryImportance returns the normalized importance score for the
// record, falling back to legacy metadata and finally a midpoint default.
func semanticMemoryImportance(record ai.SemanticRecord) int {
	if record.Profile.Importance > 0 {
		return record.Profile.Importance
	}
	if raw := strings.TrimSpace(record.Metadata[ai.SemanticMetadataImportance]); raw != "" {
		if value, err := strconv.Atoi(raw); err == nil {
			return value
		}
	}

	return 5
}

// semanticMemoryLastAccessed returns the most trustworthy last-access time for
// the record in UTC, falling back to legacy metadata and finally the creation
// time.
func semanticMemoryLastAccessed(record ai.SemanticRecord) time.Time {
	if !record.Profile.LastAccessedAt.IsZero() {
		return record.Profile.LastAccessedAt.UTC()
	}
	if raw := strings.TrimSpace(record.Metadata[ai.SemanticMetadataLastAccessed]); raw != "" {
		if parsed, err := time.Parse(time.RFC3339, raw); err == nil {
			return parsed.UTC()
		}
	}

	return record.CreatedAt.UTC()
}

// semanticMemoryActorWeight returns the actor-based score multiplier for a
// record given the current actor and the reply-chain related actors.
func semanticMemoryActorWeight(
	record ai.SemanticRecord,
	currentActor ai.SemanticActorRef,
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

// semanticMemoryActorMatches reports whether ref refers to actor, comparing
// IDs exactly and names case-insensitively.
func semanticMemoryActorMatches(ref *ai.SemanticActorRef, actor ai.SemanticActorRef) bool {
	if ref == nil {
		return false
	}
	if ref.ID != "" && ref.ID == strings.TrimSpace(actor.ID) {
		return true
	}
	refName := strings.ToLower(strings.TrimSpace(ref.Name))
	actorName := strings.ToLower(strings.TrimSpace(actor.Name))
	if refName != "" && actorName != "" && refName == actorName {
		return true
	}

	return false
}

// semanticMemoryActorInSet reports whether ref is present in the related-actor
// lookup set.
func semanticMemoryActorInSet(ref *ai.SemanticActorRef, values map[string]struct{}) bool {
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

// semanticMemoryMetadata rebuilds the legacy metadata map from the typed
// profile so that downstream consumers that only read metadata see fresh
// values after reinforcement.
func semanticMemoryMetadata(existing map[string]string, profile ai.SemanticProfile) map[string]string {
	metadata := cloneStringMap(existing)
	if metadata == nil {
		metadata = make(map[string]string)
	}
	metadata[ai.SemanticMetadataImportance] = fmt.Sprintf("%d", semanticMemoryImportance(ai.SemanticRecord{Profile: profile}))
	metadata[ai.SemanticMetadataAccessCount] = fmt.Sprintf("%d", profile.AccessCount)
	metadata[ai.SemanticMetadataLastAccessed] = profile.LastAccessedAt.UTC().Format(time.RFC3339)
	if strings.TrimSpace(profile.Source) != "" {
		metadata[ai.SemanticMetadataSource] = strings.TrimSpace(profile.Source)
	}
	if strings.TrimSpace(profile.SourceArticleID) != "" {
		metadata[ai.SemanticMetadataSourceArticleID] = strings.TrimSpace(profile.SourceArticleID)
	}
	if profile.SourceActor != nil {
		if profile.SourceActor.ID != "" {
			metadata[ai.SemanticMetadataSourceActorID] = profile.SourceActor.ID
		}
		if profile.SourceActor.Name != "" {
			metadata[ai.SemanticMetadataSourceActorName] = profile.SourceActor.Name
		}
		metadata[ai.SemanticMetadataSourceActorIsBot] = fmt.Sprintf("%t", profile.SourceActor.IsBot)
	}
	if profile.SubjectActor != nil {
		if profile.SubjectActor.ID != "" {
			metadata[ai.SemanticMetadataSubjectActorID] = profile.SubjectActor.ID
		}
		if profile.SubjectActor.Name != "" {
			metadata[ai.SemanticMetadataSubjectActorName] = profile.SubjectActor.Name
		}
		metadata[ai.SemanticMetadataSubjectActorIsBot] = fmt.Sprintf("%t", profile.SubjectActor.IsBot)
	}
	if len(profile.EvidenceRecordIDs) > 0 {
		metadata[ai.SemanticMetadataSourceRecordIDs] = strings.Join(profile.EvidenceRecordIDs, ",")
	}

	return metadata
}

// maxSemanticMemorySearchLimit returns the per-query search cap given the base
// policy limit, plan size, and requested depth.
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

// normalizeTimeFilter validates the planner time filter and returns the
// canonical lowercase form, or empty when unsupported.
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

// normalizeDepth validates the planner depth value and returns the canonical
// lowercase form, or empty when the default is requested.
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

// filterMatchesByTime keeps matches whose CreatedAt falls within the requested
// time window.
func filterMatchesByTime(matches []ai.SemanticMatch, filter string, now time.Time) []ai.SemanticMatch {
	var cutoff time.Time
	switch filter {
	case "recent":
		cutoff = now.Add(-24 * time.Hour)
	case "last_week":
		cutoff = now.Add(-7 * 24 * time.Hour)
	default:
		return matches
	}

	filtered := make([]ai.SemanticMatch, 0, len(matches))
	for _, match := range matches {
		if !match.Record.CreatedAt.Before(cutoff) {
			filtered = append(filtered, match)
		}
	}

	return filtered
}

// extractQueryTerms returns a deduplicated slice of lowercase significant
// words from the planner queries, used for keyword-overlap boosting.
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

// maxInt returns the larger of the two integer arguments.
func maxInt(left int, right int) int {
	if left > right {
		return left
	}

	return right
}

// extractJSONObject returns the first balanced JSON object substring within
// text, trimmed of surrounding whitespace.
func extractJSONObject(text string) (string, error) {
	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start < 0 || end <= start {
		return "", fmt.Errorf("json object not found")
	}

	return strings.TrimSpace(text[start : end+1]), nil
}
