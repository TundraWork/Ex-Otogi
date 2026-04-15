package openai

import (
	"context"
	"errors"
	"io"
	"testing"

	"ex-otogi/pkg/otogi/ai"
	panel "ex-otogi/pkg/otogi/management"

	"github.com/openai/openai-go/v3/responses"
)

func TestOpenAIProviderObservabilityEmitsEventsAndArtifacts(t *testing.T) {
	t.Parallel()

	recorder := &managementRecorderStub{}
	client := &openAIResponsesClientStub{
		stream: &openAIResponseStreamStub{
			events: []responses.ResponseStreamEventUnion{
				mustUnmarshalEvent(t, `{"type":"response.output_text.delta","delta":"hello"}`),
				mustUnmarshalEvent(t, `{"type":"response.completed","response":{}}`),
			},
		},
	}
	provider := &Provider{responses: client}
	ctx := panel.WithRecorder(panel.WithTrace(context.Background(), panel.TraceContext{TraceID: "trace-openai"}), recorder)

	stream, err := provider.GenerateStream(ctx, ai.LLMGenerateRequest{
		Model: "gpt-5-mini",
		Messages: []ai.LLMMessage{
			{Role: ai.LLMMessageRoleUser, Content: "hello"},
		},
	})
	if err != nil {
		t.Fatalf("GenerateStream failed: %v", err)
	}
	for {
		_, recvErr := stream.Recv(context.Background())
		if errors.Is(recvErr, io.EOF) {
			break
		}
		if recvErr != nil {
			t.Fatalf("Recv failed: %v", recvErr)
		}
	}

	if len(recorder.events) != 2 {
		t.Fatalf("event count = %d, want 2", len(recorder.events))
	}
	if recorder.events[0].Kind != "llm.call.started" || recorder.events[1].Kind != "llm.call.completed" {
		t.Fatalf("event kinds = [%q,%q], want llm.call.started/completed", recorder.events[0].Kind, recorder.events[1].Kind)
	}
	if recorder.events[0].Description != "1 messages, 0 tools: hello" {
		t.Fatalf("start description = %q, want request preview", recorder.events[0].Description)
	}
	if recorder.events[1].Description != "hello" {
		t.Fatalf("completed description = %q, want hello", recorder.events[1].Description)
	}
	if len(recorder.artifacts) < 2 {
		t.Fatalf("artifact count = %d, want at least 2", len(recorder.artifacts))
	}
}

func TestOpenAIEmbeddingObservabilityEmitsFailureEvent(t *testing.T) {
	t.Parallel()

	recorder := &managementRecorderStub{}
	provider := &EmbeddingProvider{
		embeddings: &openAIEmbeddingsClientStub{err: errors.New("boom")},
		defaults: embeddingDefaults{
			model:      defaultEmbeddingModel,
			dimensions: defaultEmbeddingDimensions,
		},
	}
	ctx := panel.WithRecorder(context.Background(), recorder)

	_, err := provider.Embed(ctx, ai.EmbeddingRequest{
		Model: "text-embedding-3-small",
		Texts: []string{"hello"},
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if len(recorder.events) != 2 {
		t.Fatalf("event count = %d, want 2", len(recorder.events))
	}
	if recorder.events[1].Kind != "embedding.call.failed" {
		t.Fatalf("last event kind = %q, want embedding.call.failed", recorder.events[1].Kind)
	}
	if recorder.events[0].Description != "1 input: hello" {
		t.Fatalf("start description = %q, want embedding request preview", recorder.events[0].Description)
	}
	if recorder.events[1].Description != "1 input: boom" {
		t.Fatalf("failure description = %q, want request summary with error", recorder.events[1].Description)
	}
}

type managementRecorderStub struct {
	events    []panel.Event
	artifacts []panel.Artifact
}

func (r *managementRecorderStub) RecordEvent(ctx context.Context, event panel.Event) (panel.Event, error) {
	event.ID = int64(len(r.events) + 1)
	if trace, ok := panel.TraceFromContext(ctx); ok {
		event.TraceID = trace.TraceID
	}
	r.events = append(r.events, event)
	return event, nil
}

func (r *managementRecorderStub) RecordArtifact(_ context.Context, artifact panel.Artifact) (panel.Artifact, error) {
	r.artifacts = append(r.artifacts, artifact)
	return artifact, nil
}

func (r *managementRecorderStub) UpsertSnapshot(context.Context, panel.Snapshot) (panel.Snapshot, error) {
	return panel.Snapshot{}, nil
}
