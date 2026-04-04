package gemini

import (
	"context"
	"errors"
	"io"
	"testing"

	"ex-otogi/pkg/otogi/ai"
	panel "ex-otogi/pkg/otogi/management"

	"google.golang.org/genai"
)

func TestGeminiProviderObservabilityEmitsEventsAndArtifacts(t *testing.T) {
	t.Parallel()

	recorder := &managementRecorderStub{}
	provider := &Provider{
		models: &modelsClientStub{
			stream: seqFromSteps([]streamStep{
				{response: textResponse([]*genai.Part{{Text: "hello"}})},
			}),
		},
		defaults: requestOptions{},
	}
	ctx := panel.WithRecorder(panel.WithTrace(context.Background(), panel.TraceContext{TraceID: "trace-gemini"}), recorder)

	stream, err := provider.GenerateStream(ctx, ai.LLMGenerateRequest{
		Model: "gemini-2.5-flash",
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
	if len(recorder.artifacts) < 2 {
		t.Fatalf("artifact count = %d, want at least 2", len(recorder.artifacts))
	}
}

func TestGeminiEmbeddingObservabilityEmitsFailureEvent(t *testing.T) {
	t.Parallel()

	recorder := &managementRecorderStub{}
	provider := &EmbeddingProvider{
		models: &geminiModelsEmbeddingClientStub{err: errors.New("boom")},
		defaults: embeddingDefaults{
			model:      defaultEmbeddingModel,
			dimensions: defaultEmbeddingDimensions,
		},
	}
	ctx := panel.WithRecorder(context.Background(), recorder)

	_, err := provider.Embed(ctx, ai.EmbeddingRequest{
		Model: "gemini-embedding-001",
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
