package otogi

import (
	"errors"
	"testing"
)

func TestMediaDownloadRequestValidate(t *testing.T) {
	t.Parallel()

	valid := MediaDownloadRequest{
		Source: EventSource{
			Platform: PlatformTelegram,
			ID:       "tg-main",
		},
		Conversation: Conversation{
			ID:   "100",
			Type: ConversationTypeGroup,
		},
		ArticleID:    "55",
		AttachmentID: "photo-1",
	}

	tests := []struct {
		name    string
		mutate  func(*MediaDownloadRequest)
		wantErr error
	}{
		{
			name: "missing source platform",
			mutate: func(request *MediaDownloadRequest) {
				request.Source.Platform = ""
			},
			wantErr: ErrInvalidMediaDownloadRequest,
		},
		{
			name: "missing source id",
			mutate: func(request *MediaDownloadRequest) {
				request.Source.ID = ""
			},
			wantErr: ErrInvalidMediaDownloadRequest,
		},
		{
			name: "missing conversation id",
			mutate: func(request *MediaDownloadRequest) {
				request.Conversation.ID = ""
			},
			wantErr: ErrInvalidMediaDownloadRequest,
		},
		{
			name: "missing conversation type",
			mutate: func(request *MediaDownloadRequest) {
				request.Conversation.Type = ""
			},
			wantErr: ErrInvalidMediaDownloadRequest,
		},
		{
			name: "missing article id",
			mutate: func(request *MediaDownloadRequest) {
				request.ArticleID = ""
			},
			wantErr: ErrInvalidMediaDownloadRequest,
		},
		{
			name: "missing attachment id",
			mutate: func(request *MediaDownloadRequest) {
				request.AttachmentID = ""
			},
			wantErr: ErrInvalidMediaDownloadRequest,
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			request := valid
			testCase.mutate(&request)

			err := request.Validate()
			if !errors.Is(err, testCase.wantErr) {
				t.Fatalf("Validate() error = %v, want %v", err, testCase.wantErr)
			}
		})
	}

	t.Run("valid", func(t *testing.T) {
		t.Parallel()

		if err := valid.Validate(); err != nil {
			t.Fatalf("Validate() error = %v, want nil", err)
		}
	})
}

func TestMediaDownloadRequestFromEvent(t *testing.T) {
	t.Parallel()

	baseEvent := &Event{
		ID:   "evt-1",
		Kind: EventKindArticleCreated,
		Source: EventSource{
			Platform: PlatformTelegram,
			ID:       "tg-main",
		},
		Conversation: Conversation{
			ID:   "100",
			Type: ConversationTypeGroup,
		},
		Article: &Article{
			ID: "55",
		},
	}

	t.Run("article event", func(t *testing.T) {
		t.Parallel()

		request, err := MediaDownloadRequestFromEvent(baseEvent, "photo-1")
		if err != nil {
			t.Fatalf("MediaDownloadRequestFromEvent() error = %v, want nil", err)
		}

		if request.Source != baseEvent.Source {
			t.Fatalf("source = %+v, want %+v", request.Source, baseEvent.Source)
		}
		if request.Conversation != baseEvent.Conversation {
			t.Fatalf("conversation = %+v, want %+v", request.Conversation, baseEvent.Conversation)
		}
		if request.ArticleID != "55" {
			t.Fatalf("article id = %q, want 55", request.ArticleID)
		}
		if request.AttachmentID != "photo-1" {
			t.Fatalf("attachment id = %q, want photo-1", request.AttachmentID)
		}
	})

	t.Run("mutation event uses target article id", func(t *testing.T) {
		t.Parallel()

		event := &Event{
			ID:   "evt-2",
			Kind: EventKindArticleEdited,
			Source: EventSource{
				Platform: PlatformTelegram,
				ID:       "tg-main",
			},
			Conversation: Conversation{
				ID:   "100",
				Type: ConversationTypeGroup,
			},
			Mutation: &ArticleMutation{
				TargetArticleID: "77",
			},
		}

		request, err := MediaDownloadRequestFromEvent(event, "doc-1")
		if err != nil {
			t.Fatalf("MediaDownloadRequestFromEvent() error = %v, want nil", err)
		}
		if request.ArticleID != "77" {
			t.Fatalf("article id = %q, want 77", request.ArticleID)
		}
	})

	t.Run("nil event", func(t *testing.T) {
		t.Parallel()

		_, err := MediaDownloadRequestFromEvent(nil, "photo-1")
		if !errors.Is(err, ErrInvalidMediaDownloadRequest) {
			t.Fatalf("MediaDownloadRequestFromEvent() error = %v, want %v", err, ErrInvalidMediaDownloadRequest)
		}
	})

	t.Run("invalid derived request", func(t *testing.T) {
		t.Parallel()

		event := *baseEvent
		event.Source.ID = ""

		_, err := MediaDownloadRequestFromEvent(&event, "photo-1")
		if !errors.Is(err, ErrInvalidMediaDownloadRequest) {
			t.Fatalf("MediaDownloadRequestFromEvent() error = %v, want %v", err, ErrInvalidMediaDownloadRequest)
		}
	})
}
