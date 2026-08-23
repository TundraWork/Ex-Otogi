package memory

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"ex-otogi/pkg/otogi/ai"
	"ex-otogi/pkg/otogi/core"
	"ex-otogi/pkg/otogi/platform"
)

// memoryServiceGetBatchStub implements core.MemoryService with a configurable
// GetBatch response. All other methods panic because tests in this file only
// exercise GetBatch.
type memoryServiceGetBatchStub struct {
	batch map[core.MemoryLookup]core.Memory
	err   error
}

func (s *memoryServiceGetBatchStub) Get(context.Context, core.MemoryLookup) (core.Memory, bool, error) {
	panic("not implemented")
}

func (s *memoryServiceGetBatchStub) GetBatch(_ context.Context, lookups []core.MemoryLookup) (map[core.MemoryLookup]core.Memory, error) {
	if s.err != nil {
		return nil, s.err
	}
	if s.batch == nil {
		return nil, nil
	}

	result := make(map[core.MemoryLookup]core.Memory, len(lookups))
	for _, l := range lookups {
		if mem, found := s.batch[l]; found {
			result[l] = mem
		}
	}

	return result, nil
}

func (s *memoryServiceGetBatchStub) GetReplied(context.Context, *platform.Event) (core.Memory, bool, error) {
	panic("not implemented")
}

func (s *memoryServiceGetBatchStub) GetReplyChain(context.Context, *platform.Event) ([]core.ReplyChainEntry, error) {
	panic("not implemented")
}

func (s *memoryServiceGetBatchStub) ListConversationContextBefore(
	context.Context,
	core.ConversationContextBeforeQuery,
) ([]core.ConversationContextEntry, error) {
	panic("not implemented")
}

func TestCollectPendingReplyLookups_NoReplies(t *testing.T) {
	t.Parallel()

	scope := ai.SemanticScope{TenantID: "t1", Platform: "telegram", ConversationID: "c1"}
	articles := []bufferedArticle{
		{Article: platform.Article{ID: "a1", Text: "hello"}},
		{Article: platform.Article{ID: "a2", Text: "world"}},
	}

	lookups := collectPendingReplyLookups(scope, articles)
	if len(lookups) != 0 {
		t.Fatalf("expected 0 lookups, got %d", len(lookups))
	}
}

func TestCollectPendingReplyLookups_ReplyInsideBuffer(t *testing.T) {
	t.Parallel()

	scope := ai.SemanticScope{TenantID: "t1", Platform: "telegram", ConversationID: "c1"}
	articles := []bufferedArticle{
		{Article: platform.Article{ID: "a1", Text: "hello"}},
		{Article: platform.Article{ID: "a2", Text: "reply", ReplyToArticleID: "a1"}},
	}

	lookups := collectPendingReplyLookups(scope, articles)
	if len(lookups) != 0 {
		t.Fatalf("expected 0 lookups when reply is inside buffer, got %d", len(lookups))
	}
}

func TestCollectPendingReplyLookups_ReplyOutsideBuffer(t *testing.T) {
	t.Parallel()

	scope := ai.SemanticScope{TenantID: "t1", Platform: "telegram", ConversationID: "c1"}
	articles := []bufferedArticle{
		{Article: platform.Article{ID: "a1", Text: "reply to outside", ReplyToArticleID: "old-msg"}},
	}

	lookups := collectPendingReplyLookups(scope, articles)
	if len(lookups) != 1 {
		t.Fatalf("expected 1 lookup, got %d", len(lookups))
	}
	if lookups[0].ArticleID != "old-msg" {
		t.Fatalf("expected lookup for old-msg, got %s", lookups[0].ArticleID)
	}
	if lookups[0].TenantID != "t1" {
		t.Fatalf("expected tenant t1, got %s", lookups[0].TenantID)
	}
	if lookups[0].Platform != platform.Platform("telegram") {
		t.Fatalf("expected platform telegram, got %s", lookups[0].Platform)
	}
	if lookups[0].ConversationID != "c1" {
		t.Fatalf("expected conversation c1, got %s", lookups[0].ConversationID)
	}
}

func TestCollectPendingReplyLookups_DedupSameReplyTarget(t *testing.T) {
	t.Parallel()

	scope := ai.SemanticScope{TenantID: "t1", Platform: "telegram", ConversationID: "c1"}
	articles := []bufferedArticle{
		{Article: platform.Article{ID: "a1", Text: "first reply", ReplyToArticleID: "old-msg"}},
		{Article: platform.Article{ID: "a2", Text: "second reply", ReplyToArticleID: "old-msg"}},
	}

	lookups := collectPendingReplyLookups(scope, articles)
	if len(lookups) != 1 {
		t.Fatalf("expected 1 deduped lookup, got %d", len(lookups))
	}
	if lookups[0].ArticleID != "old-msg" {
		t.Fatalf("expected lookup for old-msg, got %s", lookups[0].ArticleID)
	}
}

func TestLoadReplyTargets_EmptyLookups(t *testing.T) {
	t.Parallel()

	result, err := loadReplyTargets(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Fatalf("expected nil result, got %v", result)
	}
}

func TestLoadReplyTargets_ResolvesAndMisses(t *testing.T) {
	t.Parallel()

	lookup1 := core.MemoryLookup{TenantID: "t1", Platform: "telegram", ConversationID: "c1", ArticleID: "a1"}
	lookup2 := core.MemoryLookup{TenantID: "t1", Platform: "telegram", ConversationID: "c1", ArticleID: "a2"}
	lookup3 := core.MemoryLookup{TenantID: "t1", Platform: "telegram", ConversationID: "c1", ArticleID: "a3"}

	stub := &memoryServiceGetBatchStub{
		batch: map[core.MemoryLookup]core.Memory{
			lookup1: {Article: platform.Article{ID: "a1", Text: "resolved 1"}},
			lookup3: {Article: platform.Article{ID: "a3", Text: "resolved 3"}},
		},
	}

	result, err := loadReplyTargets(context.Background(), stub, []core.MemoryLookup{lookup1, lookup2, lookup3})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 2 {
		t.Fatalf("expected 2 resolved entries, got %d", len(result))
	}
	if _, found := result["a1"]; !found {
		t.Fatal("expected a1 in results")
	}
	if _, found := result["a3"]; !found {
		t.Fatal("expected a3 in results")
	}
	if _, found := result["a2"]; found {
		t.Fatal("did not expect a2 in results")
	}
}

func TestLoadReplyTargets_Error(t *testing.T) {
	t.Parallel()

	stub := &memoryServiceGetBatchStub{err: fmt.Errorf("db down")}
	lookup := core.MemoryLookup{TenantID: "t1", Platform: "telegram", ConversationID: "c1", ArticleID: "a1"}

	_, err := loadReplyTargets(context.Background(), stub, []core.MemoryLookup{lookup})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "load reply targets") {
		t.Fatalf("expected wrapped error, got: %v", err)
	}
}

func TestSerializeWindowConversation_BasicChronological(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 4, 6, 12, 0, 0, 0, time.UTC)
	articles := []bufferedArticle{
		{
			Article:    platform.Article{ID: "m3", Text: "third"},
			Actor:      platform.Actor{ID: "u1", DisplayName: "alice"},
			OccurredAt: base.Add(2 * time.Minute),
		},
		{
			Article:    platform.Article{ID: "m1", Text: "first"},
			Actor:      platform.Actor{ID: "u1", DisplayName: "alice"},
			OccurredAt: base,
		},
		{
			Article:    platform.Article{ID: "m2", Text: "second"},
			Actor:      platform.Actor{ID: "u2", DisplayName: "bob", IsBot: true},
			OccurredAt: base.Add(time.Minute),
		},
	}

	text, omitted := serializeWindowConversation(articles, nil, 100000)
	if omitted != 0 {
		t.Fatalf("expected 0 omitted, got %d", omitted)
	}
	if !strings.Contains(text, "<conversation>") {
		t.Fatal("missing <conversation> envelope")
	}
	if !strings.Contains(text, "</conversation>") {
		t.Fatal("missing </conversation> envelope")
	}

	// Verify chronological order: m1 before m2 before m3.
	m1Pos := strings.Index(text, `article_id="m1"`)
	m2Pos := strings.Index(text, `article_id="m2"`)
	m3Pos := strings.Index(text, `article_id="m3"`)
	if m1Pos < 0 || m2Pos < 0 || m3Pos < 0 {
		t.Fatalf("missing article IDs in output:\n%s", text)
	}
	if m1Pos >= m2Pos || m2Pos >= m3Pos {
		t.Fatalf("articles not in chronological order: m1=%d m2=%d m3=%d", m1Pos, m2Pos, m3Pos)
	}

	// Verify is_bot attribute.
	if !strings.Contains(text, `is_bot="true"`) {
		t.Fatal("missing is_bot=true for bot actor")
	}
	if !strings.Contains(text, `is_bot="false"`) {
		t.Fatal("missing is_bot=false for non-bot actor")
	}

	// No quoted_reply since there are no replies.
	if strings.Contains(text, "<quoted_reply") {
		t.Fatal("unexpected <quoted_reply> in output with no replies")
	}
}

func TestSerializeWindowConversation_WithQuotedReply(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 4, 6, 11, 0, 0, 0, time.UTC)
	articles := []bufferedArticle{
		{
			Article:    platform.Article{ID: "m7", Text: "Let's push it to Wednesday", ReplyToArticleID: "msg-old"},
			Actor:      platform.Actor{ID: "456", DisplayName: "bob"},
			OccurredAt: base.Add(150 * time.Minute),
		},
		{
			Article:    platform.Article{ID: "m8", Text: "Works for me"},
			Actor:      platform.Actor{ID: "123", DisplayName: "alice"},
			OccurredAt: base.Add(151 * time.Minute),
		},
	}

	quotes := map[string]core.Memory{
		"msg-old": {
			Article:   platform.Article{ID: "msg-old", Text: "Meeting scheduled for Tuesday"},
			Actor:     platform.Actor{ID: "123", DisplayName: "alice"},
			CreatedAt: base,
		},
	}

	text, omitted := serializeWindowConversation(articles, quotes, 100000)
	if omitted != 0 {
		t.Fatalf("expected 0 omitted, got %d", omitted)
	}

	// Verify quoted_reply appears before the replying message.
	quotePos := strings.Index(text, "<quoted_reply")
	msgPos := strings.Index(text, `article_id="m7"`)
	if quotePos < 0 {
		t.Fatalf("missing <quoted_reply> in output:\n%s", text)
	}
	if quotePos >= msgPos {
		t.Fatalf("quoted_reply should appear before the replying message")
	}

	// Verify quoted_reply attributes come from the QUOTED article/actor.
	if !strings.Contains(text, `article_id="msg-old"`) {
		t.Fatal("quoted_reply missing article_id of the quoted message")
	}
	if !strings.Contains(text, `actor_id="123"`) {
		t.Fatal("quoted_reply missing actor_id of the quoted actor")
	}
	if !strings.Contains(text, `actor_name="alice"`) {
		t.Fatal("quoted_reply missing actor_name of the quoted actor")
	}
	if !strings.Contains(text, "Meeting scheduled for Tuesday") {
		t.Fatal("quoted_reply missing resolved text body")
	}

	// The message should have reply_to attribute.
	if !strings.Contains(text, `reply_to="msg-old"`) {
		t.Fatal("message missing reply_to attribute")
	}
}

func TestSerializeWindowConversation_UnresolvedReplyNoQuote(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 4, 6, 12, 0, 0, 0, time.UTC)
	articles := []bufferedArticle{
		{
			Article:    platform.Article{ID: "m1", Text: "replying to missing", ReplyToArticleID: "ghost"},
			Actor:      platform.Actor{ID: "u1", DisplayName: "alice"},
			OccurredAt: base,
		},
	}

	// Empty quotes — the reply target is unresolved.
	text, omitted := serializeWindowConversation(articles, nil, 100000)
	if omitted != 0 {
		t.Fatalf("expected 0 omitted, got %d", omitted)
	}

	// No quoted_reply element.
	if strings.Contains(text, "<quoted_reply") {
		t.Fatal("unexpected <quoted_reply> for unresolved reply target")
	}

	// But the message still has reply_to attribute.
	if !strings.Contains(text, `reply_to="ghost"`) {
		t.Fatal("message missing reply_to attribute for unresolved target")
	}
}

func TestSerializeWindowConversation_RuneBudget(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 4, 6, 12, 0, 0, 0, time.UTC)
	articles := make([]bufferedArticle, 5)
	for i := range articles {
		articles[i] = bufferedArticle{
			Article:    platform.Article{ID: fmt.Sprintf("m%d", i+1), Text: fmt.Sprintf("Message number %d with some padding text", i+1)},
			Actor:      platform.Actor{ID: fmt.Sprintf("u%d", i+1), DisplayName: fmt.Sprintf("user%d", i+1)},
			OccurredAt: base.Add(time.Duration(i) * time.Minute),
		}
	}

	// Render all to get the full size, then use a budget that forces dropping.
	fullText, fullOmitted := serializeWindowConversation(articles, nil, 1000000)
	if fullOmitted != 0 {
		t.Fatalf("full render should omit 0, got %d", fullOmitted)
	}
	if fullText == "" {
		t.Fatal("full render produced empty text")
	}

	// Use a budget that can fit ~2 messages + envelope.
	// Render with only the last article to find the minimum single-article size.
	singleText, _ := serializeWindowConversation(articles[4:], nil, 1000000)
	singleRunes := len([]rune(singleText))

	// Budget allows roughly 2 messages: single * 2 + some headroom.
	budget := singleRunes*2 + 50
	text, omitted := serializeWindowConversation(articles, nil, budget)
	if omitted == 0 {
		t.Fatal("expected some articles to be dropped with small budget")
	}
	if omitted >= len(articles) {
		t.Fatalf("expected at least one article to remain, omitted=%d", omitted)
	}
	if text == "" {
		t.Fatal("expected non-empty text when budget allows some articles")
	}

	// Verify the last article is always present (newest is kept).
	if !strings.Contains(text, `article_id="m5"`) {
		t.Fatal("expected newest article (m5) to be present")
	}
}

func TestSerializeWindowConversation_ZeroBudget(t *testing.T) {
	t.Parallel()

	articles := []bufferedArticle{
		{Article: platform.Article{ID: "m1", Text: "hello"}, Actor: platform.Actor{ID: "u1"}},
	}
	text, omitted := serializeWindowConversation(articles, nil, 0)
	if text != "" {
		t.Fatal("expected empty text with zero budget")
	}
	if omitted != 1 {
		t.Fatalf("expected omitted=1, got %d", omitted)
	}
}

func TestSerializeWindowConversation_EmptyArticles(t *testing.T) {
	t.Parallel()

	text, omitted := serializeWindowConversation(nil, nil, 1000)
	if text != "" {
		t.Fatal("expected empty text for nil articles")
	}
	if omitted != 0 {
		t.Fatalf("expected omitted=0 for nil articles, got %d", omitted)
	}
}

func TestParticipantsFromArticles(t *testing.T) {
	t.Parallel()

	articles := []bufferedArticle{
		{Actor: platform.Actor{ID: "u1", DisplayName: "Alice"}},
		{Actor: platform.Actor{ID: "u2", Username: "bob_user"}},
		{Actor: platform.Actor{ID: "u1", DisplayName: "Alice"}}, // duplicate
	}

	quotes := map[string]core.Memory{
		"q1": {Actor: platform.Actor{ID: "u3", DisplayName: "Charlie"}},
		"q2": {Actor: platform.Actor{ID: "u1", DisplayName: "Alice"}}, // duplicate with articles
	}

	participants := participantsFromArticles(articles, quotes)

	// Should have 3 unique actors: u1/Alice, u2/bob_user, u3/Charlie.
	if len(participants) != 3 {
		t.Fatalf("expected 3 unique participants, got %d: %+v", len(participants), participants)
	}

	ids := make(map[string]bool, len(participants))
	for _, p := range participants {
		ids[p.ID] = true
	}
	for _, expected := range []string{"u1", "u2", "u3"} {
		if !ids[expected] {
			t.Fatalf("expected participant %s not found", expected)
		}
	}
}

func TestParticipantsFromArticles_NameFallback(t *testing.T) {
	t.Parallel()

	articles := []bufferedArticle{
		{Actor: platform.Actor{ID: "u1", Username: "user_handle"}}, // no DisplayName, falls back to Username
	}

	participants := participantsFromArticles(articles, nil)
	if len(participants) != 1 {
		t.Fatalf("expected 1 participant, got %d", len(participants))
	}
	if participants[0].Name != "user_handle" {
		t.Fatalf("expected name 'user_handle', got %q", participants[0].Name)
	}
}
