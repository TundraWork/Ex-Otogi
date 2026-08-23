package management

import "context"

type recorderContextKey struct{}

// WithRecorder returns a child context carrying the provided management
// recorder.
func WithRecorder(ctx context.Context, recorder Recorder) context.Context {
	return context.WithValue(ctx, recorderContextKey{}, recorder)
}

// RecorderFromContext extracts a recorder previously attached with WithRecorder.
func RecorderFromContext(ctx context.Context) (Recorder, bool) {
	if ctx == nil {
		return nil, false
	}

	recorder, ok := ctx.Value(recorderContextKey{}).(Recorder)
	return recorder, ok && recorder != nil
}
