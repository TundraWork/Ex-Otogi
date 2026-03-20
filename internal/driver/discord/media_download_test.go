package discord

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"ex-otogi/pkg/otogi/platform"
)

// mockHTTPClient is a controllable http.Client for media download tests.
type mockHTTPClient struct {
	doFn func(req *http.Request) (*http.Response, error)
}

func (m *mockHTTPClient) Do(req *http.Request) (*http.Response, error) {
	if m.doFn != nil {
		return m.doFn(req)
	}

	return &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(strings.NewReader("file content")),
		Header:     http.Header{"Content-Type": []string{"image/png"}},
	}, nil
}

func defaultMediaDownloadRequest() platform.MediaDownloadRequest {
	return platform.MediaDownloadRequest{
		Source:       platform.EventSource{Platform: DriverPlatform, ID: "bot-1"},
		Conversation: platform.Conversation{ID: "c-1", Type: platform.ConversationTypeGroup},
		ArticleID:    "msg-1",
		AttachmentID: "https://cdn.discordapp.com/attachments/1/2/file.png",
	}
}

func TestMediaDownloader_Download(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	tests := []struct {
		name          string
		request       platform.MediaDownloadRequest
		doFn          func(req *http.Request) (*http.Response, error)
		wantErr       bool
		wantErrSubstr string
		wantMIME      string
		wantBody      string
	}{
		{
			name:     "successful download",
			request:  defaultMediaDownloadRequest(),
			wantMIME: "image/png",
			wantBody: "file content",
		},
		{
			name:    "http error is wrapped",
			request: defaultMediaDownloadRequest(),
			doFn: func(_ *http.Request) (*http.Response, error) {
				return nil, io.EOF
			},
			wantErr: true,
		},
		{
			name:    "non-200 status returns error",
			request: defaultMediaDownloadRequest(),
			doFn: func(_ *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: 404,
					Body:       io.NopCloser(strings.NewReader("")),
				}, nil
			},
			wantErr:       true,
			wantErrSubstr: "404",
		},
		{
			name: "invalid request returns error",
			request: platform.MediaDownloadRequest{
				Source:       platform.EventSource{Platform: "", ID: ""},
				Conversation: platform.Conversation{},
			},
			wantErr: true,
		},
		{
			name:    "nil output returns error",
			request: defaultMediaDownloadRequest(),
			wantErr: true,
		},
		{
			name: "empty attachment ID returns not found",
			request: platform.MediaDownloadRequest{
				Source:       platform.EventSource{Platform: DriverPlatform, ID: "bot-1"},
				Conversation: platform.Conversation{ID: "c-1", Type: platform.ConversationTypeGroup},
				ArticleID:    "msg-1",
				AttachmentID: "",
			},
			wantErr: true,
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			client := &mockHTTPClient{doFn: testCase.doFn}
			d := newMediaDownloaderWithClient(client)

			var output bytes.Buffer
			var outputWriter io.Writer = &output
			if testCase.name == "nil output returns error" {
				outputWriter = nil
			}

			attachment, err := d.Download(ctx, testCase.request, outputWriter)
			if testCase.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if testCase.wantErrSubstr != "" && !containsStr(err.Error(), testCase.wantErrSubstr) {
					t.Errorf("error %q does not contain %q", err.Error(), testCase.wantErrSubstr)
				}

				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if testCase.wantMIME != "" && attachment.MIMEType != testCase.wantMIME {
				t.Errorf("MIMEType = %q, want %q", attachment.MIMEType, testCase.wantMIME)
			}
			if testCase.wantBody != "" && output.String() != testCase.wantBody {
				t.Errorf("body = %q, want %q", output.String(), testCase.wantBody)
			}
		})
	}
}
