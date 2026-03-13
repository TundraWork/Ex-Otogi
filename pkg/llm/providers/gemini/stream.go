package gemini

import (
	"context"
	"errors"
	"fmt"
	"io"
	"iter"
	"log/slog"
	"strings"
	"sync"

	"ex-otogi/pkg/otogi/ai"

	"google.golang.org/genai"
)

type geminiStream struct {
	mu sync.Mutex

	next func() (*genai.GenerateContentResponse, error, bool)
	stop func()

	closed          bool
	finished        bool
	includeThoughts bool
	pending         []ai.LLMGenerateChunk
	logger          *slog.Logger
	request         geminiRequestDiagnostics

	seenResponses           int
	observedOutputTextParts int
	lastResponseDiagnostics geminiResponseDiagnostics
}

func newGeminiStream(
	seq iter.Seq2[*genai.GenerateContentResponse, error],
	includeThoughts bool,
	logger *slog.Logger,
	request geminiRequestDiagnostics,
) *geminiStream {
	next, stop := iter.Pull2(seq)
	return &geminiStream{
		next:            next,
		stop:            stop,
		includeThoughts: includeThoughts,
		logger:          resolveLogger(logger),
		request:         request,
	}
}

func (s *geminiStream) Recv(ctx context.Context) (ai.LLMGenerateChunk, error) {
	if ctx == nil {
		return ai.LLMGenerateChunk{}, fmt.Errorf("gemini stream recv: nil context")
	}

	for {
		if err := ctx.Err(); err != nil {
			_ = s.Close()
			return ai.LLMGenerateChunk{}, fmt.Errorf("gemini stream recv context: %w", err)
		}
		if chunk, ok := s.dequeuePending(); ok {
			if chunk.Delta == "" {
				continue
			}
			return chunk, nil
		}

		response, err := s.nextResponse(ctx)
		if err != nil {
			return ai.LLMGenerateChunk{}, err
		}

		summary := summarizeGenerateContentResponse(response)
		s.recordResponse(summary)

		chunks, mapErr := mapGenerateContentResponse(response, s.includeThoughts)
		if mapErr != nil {
			s.logResponseError(ctx, mapErr, summary)
			return ai.LLMGenerateChunk{}, mapErr
		}
		if len(chunks) == 0 {
			continue
		}
		if len(chunks) > 1 {
			s.enqueuePending(chunks[1:])
		}

		if chunks[0].Delta == "" {
			continue
		}
		return chunks[0], nil
	}
}

func (s *geminiStream) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.finished = true
	stop := s.stop
	s.stop = nil
	s.next = nil
	s.mu.Unlock()

	if stop != nil {
		stop()
	}

	return nil
}

func (s *geminiStream) nextResponse(ctx context.Context) (*genai.GenerateContentResponse, error) {
	s.mu.Lock()
	if s.closed || s.finished {
		s.mu.Unlock()
		return nil, io.EOF
	}
	next := s.next
	if next == nil {
		s.finished = true
		s.mu.Unlock()
		return nil, io.EOF
	}
	s.mu.Unlock()

	response, recvErr, ok := next()
	if !ok {
		s.markFinished()
		s.logEmptyCompletion(ctx)
		return nil, io.EOF
	}
	if recvErr != nil {
		s.markFinished()
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, fmt.Errorf("gemini stream context: %w", ctxErr)
		}
		if errors.Is(recvErr, context.Canceled) || errors.Is(recvErr, context.DeadlineExceeded) {
			return nil, fmt.Errorf("gemini stream canceled: %w", recvErr)
		}
		s.logReceiveError(ctx, recvErr)
		return nil, fmt.Errorf("gemini stream next: %w", recvErr)
	}

	return response, nil
}

func (s *geminiStream) markFinished() {
	s.mu.Lock()
	s.finished = true
	s.mu.Unlock()
}

func (s *geminiStream) dequeuePending() (ai.LLMGenerateChunk, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.pending) == 0 {
		return ai.LLMGenerateChunk{}, false
	}

	chunk := s.pending[0]
	s.pending = s.pending[1:]
	return chunk, true
}

func (s *geminiStream) enqueuePending(chunks []ai.LLMGenerateChunk) {
	if len(chunks) == 0 {
		return
	}

	s.mu.Lock()
	s.pending = append(s.pending, chunks...)
	s.mu.Unlock()
}

func (s *geminiStream) recordResponse(summary geminiResponseDiagnostics) {
	s.mu.Lock()
	s.seenResponses++
	s.observedOutputTextParts += summary.outputTextParts
	s.lastResponseDiagnostics = summary
	s.mu.Unlock()
}

func (s *geminiStream) snapshotDiagnostics() (int, int, geminiResponseDiagnostics) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.seenResponses, s.observedOutputTextParts, s.lastResponseDiagnostics
}

func (s *geminiStream) logReceiveError(ctx context.Context, err error) {
	if s == nil || s.logger == nil || err == nil {
		return
	}

	args := s.request.slogArgs()
	_, _, response := s.snapshotDiagnostics()
	args = append(args, response.slogArgs()...)

	var apiErr genai.APIError
	if errors.As(err, &apiErr) {
		args = append(args,
			"api_code", apiErr.Code,
			"api_status", apiErr.Status,
			"api_message", strings.TrimSpace(apiErr.Message),
			"api_details", apiErr.Details,
		)
	}
	args = append(args, "error", err)
	s.logger.ErrorContext(ctx, "gemini API stream receive failed", args...)
}

func (s *geminiStream) logResponseError(
	ctx context.Context,
	err error,
	summary geminiResponseDiagnostics,
) {
	if s == nil || s.logger == nil || err == nil {
		return
	}

	args := s.request.slogArgs()
	args = append(args, summary.slogArgs()...)
	args = append(args, "error", err)
	s.logger.ErrorContext(ctx, "gemini API returned an invalid or blocked response", args...)
}

func (s *geminiStream) logEmptyCompletion(ctx context.Context) {
	if s == nil || s.logger == nil {
		return
	}

	seenResponses, observedOutputTextParts, summary := s.snapshotDiagnostics()
	if seenResponses == 0 || observedOutputTextParts > 0 {
		return
	}

	args := s.request.slogArgs()
	args = append(args, "seen_responses", seenResponses)
	args = append(args, summary.slogArgs()...)
	s.logger.WarnContext(ctx, "gemini stream completed without output text", args...)
}

type geminiRequestDiagnostics struct {
	model           string
	messageCount    int
	systemMessages  int
	maxOutputTokens int
	temperature     float64
	googleSearch    bool
	urlContext      bool
	includeThoughts bool
	thinkingBudget  *int32
	thinkingLevel   string
	responseMIME    string
	safetyFilterOff bool
}

func newGeminiRequestDiagnostics(
	req ai.LLMGenerateRequest,
	options requestOptions,
) geminiRequestDiagnostics {
	diagnostics := geminiRequestDiagnostics{
		model:           strings.TrimSpace(req.Model),
		messageCount:    len(req.Messages),
		maxOutputTokens: req.MaxOutputTokens,
		temperature:     req.Temperature,
		googleSearch:    isTrue(options.googleSearch),
		urlContext:      isTrue(options.urlContext),
		includeThoughts: isTrue(options.includeThoughts),
		thinkingBudget:  cloneInt32Pointer(options.thinkingBudget),
		thinkingLevel:   strings.TrimSpace(string(options.thinkingLevel)),
		responseMIME:    strings.TrimSpace(options.responseMIME),
		safetyFilterOff: isTrue(options.safetyFilterOff),
	}
	for _, message := range req.Messages {
		if message.Role == ai.LLMMessageRoleSystem {
			diagnostics.systemMessages++
		}
	}

	return diagnostics
}

func (d geminiRequestDiagnostics) slogArgs() []any {
	args := []any{
		"model", d.model,
		"message_count", d.messageCount,
		"system_message_count", d.systemMessages,
		"google_search", d.googleSearch,
		"url_context", d.urlContext,
		"include_thoughts", d.includeThoughts,
		"safety_filter_off", d.safetyFilterOff,
	}
	if d.maxOutputTokens > 0 {
		args = append(args, "max_output_tokens", d.maxOutputTokens)
	}
	if d.temperature > 0 {
		args = append(args, "temperature", d.temperature)
	}
	if d.thinkingBudget != nil {
		args = append(args, "thinking_budget", *d.thinkingBudget)
	}
	if d.thinkingLevel != "" {
		args = append(args, "thinking_level", d.thinkingLevel)
	}
	if d.responseMIME != "" {
		args = append(args, "response_mime_type", d.responseMIME)
	}

	return args
}

type geminiResponseDiagnostics struct {
	candidateCount         int
	promptBlockReason      string
	promptBlockMessage     string
	promptSafetyRatings    []string
	finishReason           string
	finishMessage          string
	candidateSafetyRatings []string
	partCount              int
	outputTextParts        int
	thoughtParts           int
	promptTokenCount       int32
	candidateTokenCount    int32
	totalTokenCount        int32
	thoughtTokenCount      int32
}

func summarizeGenerateContentResponse(response *genai.GenerateContentResponse) geminiResponseDiagnostics {
	if response == nil {
		return geminiResponseDiagnostics{}
	}

	summary := geminiResponseDiagnostics{
		candidateCount: len(response.Candidates),
	}
	if response.PromptFeedback != nil {
		summary.promptBlockReason = strings.TrimSpace(string(response.PromptFeedback.BlockReason))
		summary.promptBlockMessage = strings.TrimSpace(response.PromptFeedback.BlockReasonMessage)
		summary.promptSafetyRatings = formatSafetyRatings(response.PromptFeedback.SafetyRatings)
	}
	if response.UsageMetadata != nil {
		summary.promptTokenCount = response.UsageMetadata.PromptTokenCount
		summary.candidateTokenCount = response.UsageMetadata.CandidatesTokenCount
		summary.totalTokenCount = response.UsageMetadata.TotalTokenCount
		summary.thoughtTokenCount = response.UsageMetadata.ThoughtsTokenCount
	}
	if len(response.Candidates) == 0 || response.Candidates[0] == nil {
		return summary
	}

	candidate := response.Candidates[0]
	summary.finishReason = strings.TrimSpace(string(candidate.FinishReason))
	summary.finishMessage = strings.TrimSpace(candidate.FinishMessage)
	summary.candidateSafetyRatings = formatSafetyRatings(candidate.SafetyRatings)
	if candidate.Content == nil {
		return summary
	}

	summary.partCount = len(candidate.Content.Parts)
	for _, part := range candidate.Content.Parts {
		if part == nil || part.Text == "" {
			continue
		}
		if part.Thought {
			summary.thoughtParts++
			continue
		}
		summary.outputTextParts++
	}

	return summary
}

func (d geminiResponseDiagnostics) slogArgs() []any {
	args := []any{
		"candidate_count", d.candidateCount,
		"candidate_part_count", d.partCount,
		"candidate_output_text_parts", d.outputTextParts,
		"candidate_thought_parts", d.thoughtParts,
	}
	if d.promptBlockReason != "" {
		args = append(args, "prompt_block_reason", d.promptBlockReason)
	}
	if d.promptBlockMessage != "" {
		args = append(args, "prompt_block_message", d.promptBlockMessage)
	}
	if len(d.promptSafetyRatings) > 0 {
		args = append(args, "prompt_safety_ratings", d.promptSafetyRatings)
	}
	if d.finishReason != "" {
		args = append(args, "candidate_finish_reason", d.finishReason)
	}
	if d.finishMessage != "" {
		args = append(args, "candidate_finish_message", d.finishMessage)
	}
	if len(d.candidateSafetyRatings) > 0 {
		args = append(args, "candidate_safety_ratings", d.candidateSafetyRatings)
	}
	if d.promptTokenCount > 0 {
		args = append(args, "prompt_tokens", d.promptTokenCount)
	}
	if d.candidateTokenCount > 0 {
		args = append(args, "candidate_tokens", d.candidateTokenCount)
	}
	if d.totalTokenCount > 0 {
		args = append(args, "total_tokens", d.totalTokenCount)
	}
	if d.thoughtTokenCount > 0 {
		args = append(args, "thought_tokens", d.thoughtTokenCount)
	}

	return args
}

func mapGenerateContentResponse(
	response *genai.GenerateContentResponse,
	includeThoughts bool,
) ([]ai.LLMGenerateChunk, error) {
	if response == nil {
		return nil, fmt.Errorf("gemini stream parse response: nil response")
	}
	if err := checkPromptFeedback(response.PromptFeedback); err != nil {
		return nil, err
	}
	if len(response.Candidates) == 0 || response.Candidates[0] == nil {
		return nil, nil
	}
	candidate := response.Candidates[0]
	if err := checkCandidateFinishReason(candidate); err != nil {
		return nil, err
	}
	content := candidate.Content
	if content == nil {
		return nil, nil
	}

	chunks := make([]ai.LLMGenerateChunk, 0, len(content.Parts))
	for _, part := range content.Parts {
		if part == nil || part.Text == "" {
			continue
		}
		if part.Thought {
			if !includeThoughts {
				continue
			}
			chunks = append(chunks, ai.LLMGenerateChunk{
				Kind:  ai.LLMGenerateChunkKindThinkingSummary,
				Delta: part.Text,
			})
			continue
		}
		chunks = append(chunks, ai.LLMGenerateChunk{
			Kind:  ai.LLMGenerateChunkKindOutputText,
			Delta: part.Text,
		})
	}

	return chunks, nil
}

// checkPromptFeedback returns an error when the API reports that the prompt
// itself was blocked (e.g. safety filters on the input side).
func checkPromptFeedback(feedback *genai.GenerateContentResponsePromptFeedback) error {
	if feedback == nil {
		return nil
	}
	reason := feedback.BlockReason
	if reason == "" || reason == genai.BlockedReasonUnspecified {
		return nil
	}

	details := []string{fmt.Sprintf("block_reason=%s", reason)}
	if feedback.BlockReasonMessage != "" {
		details = append(details, fmt.Sprintf("message=%q", feedback.BlockReasonMessage))
	}
	details = append(details, formatSafetyRatings(feedback.SafetyRatings)...)

	return fmt.Errorf("gemini prompt blocked: %s", strings.Join(details, " "))
}

// checkCandidateFinishReason returns an error when a candidate was terminated
// for a non-normal reason such as SAFETY, RECITATION, or MAX_TOKENS.
func checkCandidateFinishReason(candidate *genai.Candidate) error {
	if candidate == nil {
		return nil
	}
	reason := candidate.FinishReason
	switch reason {
	case "", genai.FinishReasonUnspecified, genai.FinishReasonStop:
		return nil
	default:
	}

	details := []string{fmt.Sprintf("finish_reason=%s", reason)}
	if candidate.FinishMessage != "" {
		details = append(details, fmt.Sprintf("message=%q", candidate.FinishMessage))
	}
	details = append(details, formatSafetyRatings(candidate.SafetyRatings)...)

	return fmt.Errorf("gemini candidate blocked: %s", strings.Join(details, " "))
}

// formatSafetyRatings formats safety rating details for error messages.
func formatSafetyRatings(ratings []*genai.SafetyRating) []string {
	var parts []string
	for _, rating := range ratings {
		if rating == nil {
			continue
		}
		entry := fmt.Sprintf(
			"[category=%s probability=%s severity=%s blocked=%t]",
			rating.Category,
			rating.Probability,
			rating.Severity,
			rating.Blocked,
		)
		parts = append(parts, entry)
	}

	return parts
}

var _ ai.LLMStream = (*geminiStream)(nil)
