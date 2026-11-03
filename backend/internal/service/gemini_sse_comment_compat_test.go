package service

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestGeminiClientRejectsSSEComments(t *testing.T) {
	cases := []struct {
		name string
		hint string
		want bool
	}{
		{"go-genai (Antigravity CLI)", "google-genai-sdk/1.71.0 gl-go/go1.28-20260721-RC03 cl/951519500 +3ebc191975 X:fieldtrack,boringcrypto", true},
		{"python-genai", "google-genai-sdk/1.20.0 gl-python/3.12.4", true},
		{"js-genai tolerates comments", "google-genai-sdk/1.9.0 gl-node/22.3.0", false},
		{"gemini-cli", "GeminiCLI/0.60.0 (darwin; arm64)", false},
		{"curl", "curl/8.7.1", false},
		{"empty", "", false},
		{"case-insensitive", "Google-GenAI-SDK/1.0.0 GL-Go/go1.27", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, geminiClientRejectsSSEComments(tc.hint))
		})
	}
}

func TestDownstreamRejectsSSECommentsReadsBothHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := newAntigravityCompatContext(http.MethodPost, "/v1beta/models/gemini-3.8-flash:streamGenerateContent", nil)
	require.False(t, downstreamRejectsSSEComments(c))

	c.Request.Header.Set("X-Goog-Api-Client", "google-genai-sdk/1.71.0 gl-go/go1.28")
	require.True(t, downstreamRejectsSSEComments(c))

	c.Request.Header.Del("X-Goog-Api-Client")
	c.Request.Header.Set("User-Agent", "google-genai-sdk/1.71.0 gl-go/go1.28")
	require.True(t, downstreamRejectsSSEComments(c))

	require.False(t, downstreamRejectsSSEComments(nil))
}

// geminiSSEEventWriter observes complete events on the stream goroutine. Tests
// wait on channels instead of reading the recorder while that goroutine writes.
type geminiSSEEventWriter struct {
	gin.ResponseWriter
	pending        string
	dataWritten    chan struct{}
	commentWritten chan struct{}
}

func (w *geminiSSEEventWriter) Write(p []byte) (int, error) {
	n, err := w.ResponseWriter.Write(p)
	w.pending += string(p[:n])
	for {
		end := strings.Index(w.pending, "\n\n")
		if end < 0 {
			break
		}
		event := w.pending[:end]
		w.pending = w.pending[end+2:]
		var signal chan struct{}
		switch {
		case strings.HasPrefix(event, "data:"):
			signal = w.dataWritten
		case strings.HasPrefix(event, ":"):
			signal = w.commentWritten
		}
		select {
		case signal <- struct{}{}:
		default:
		}
	}
	return n, err
}

func (w *geminiSSEEventWriter) WriteString(s string) (int, error) {
	return w.Write([]byte(s))
}

// runAntigravityGeminiStreamWithIdle sends one data event, then holds the
// upstream open while observing the idle stream. The first 1s ticker event can
// precede lastDataAt+1s, so closing at 1.2s races the second eligible tick.
func runAntigravityGeminiStreamWithIdle(t *testing.T, userAgent string, wantKeepalive bool) string {
	t.Helper()
	gin.SetMode(gin.TestMode)
	const keepaliveInterval = time.Second
	const eventTimeout = 5 * keepaliveInterval
	svc := newAntigravityCompatService(
		config.GatewayConfig{MaxLineSize: defaultMaxLineSize, StreamKeepaliveInterval: 1}, nil,
	)
	c, recorder := newAntigravityCompatContext(http.MethodPost, "/v1beta/models/gemini-3.8-flash:streamGenerateContent", nil)
	if userAgent != "" {
		c.Request.Header.Set("User-Agent", userAgent)
	}
	observer := &geminiSSEEventWriter{
		ResponseWriter: c.Writer, dataWritten: make(chan struct{}, 1), commentWritten: make(chan struct{}, 1),
	}
	c.Writer = observer
	reader, writer := io.Pipe()
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: reader}
	done := make(chan struct{})
	var streamErr error
	go func() {
		_, streamErr = svc.handleGeminiStreamingResponse(c, resp, time.Now())
		close(done)
	}()
	t.Cleanup(func() {
		_ = writer.Close()
		_ = reader.Close()
		select {
		case <-done:
		case <-time.After(eventTimeout):
			t.Error("stream goroutine did not exit after pipe cleanup")
		}
	})
	waitForEvent := func(signal <-chan struct{}, description string) {
		t.Helper()
		timer := time.NewTimer(eventTimeout)
		defer timer.Stop()
		select {
		case <-signal:
		case <-done:
			t.Fatalf("stream ended before %s: %v", description, streamErr)
		case <-timer.C:
			t.Fatalf("timed out waiting for %s", description)
		}
	}
	writeDone := make(chan error, 1)
	go func() {
		_, err := io.WriteString(writer,
			`data: {"response":{"responseId":"resp_1","candidates":[{"content":{"parts":[{"text":"partial"}]}}],"usageMetadata":{"promptTokenCount":8,"candidatesTokenCount":1}}}`+"\n\n")
		writeDone <- err
	}()
	select {
	case err := <-writeDone:
		require.NoError(t, err)
	case <-time.After(eventTimeout):
		t.Fatal("timed out sending initial upstream event")
	}
	waitForEvent(observer.dataWritten, "initial downstream data event")
	if wantKeepalive {
		// Keep the upstream open until the actual comment arrives, with a hard bound.
		waitForEvent(observer.commentWritten, "idle comment keepalive")
	} else {
		// Start only after data delivery and cover multiple ticker opportunities, not
		// merely the first tick that can be skipped by the idle-duration check.
		timer := time.NewTimer(3 * keepaliveInterval)
		defer timer.Stop()
		select {
		case <-observer.commentWritten:
			t.Fatal("go-genai received a forbidden SSE comment while idle")
		case <-done:
			t.Fatalf("stream ended before the no-comment observation window: %v", streamErr)
		case <-timer.C:
		}
	}
	require.NoError(t, writer.Close())
	select {
	case <-done:
		require.NoError(t, streamErr)
	case <-time.After(eventTimeout):
		t.Fatal("stream did not finish after upstream EOF")
	}
	// The done channel joins the only recorder writer before reading its body.
	return recorder.Body.String()
}

func TestAntigravityGeminiStreamKeepsCommentKeepaliveForOrdinaryClients(t *testing.T) {
	out := runAntigravityGeminiStreamWithIdle(t, "curl/8.7.1", true)
	require.Contains(t, out, ":\n\n", "ordinary clients should still get the idle keepalive")
	require.Contains(t, out, `"text":"partial"`)
}

func TestAntigravityGeminiStreamSkipsCommentKeepaliveForGoGenai(t *testing.T) {
	out := runAntigravityGeminiStreamWithIdle(t, "google-genai-sdk/1.71.0 gl-go/go1.28-20260721-RC03", false)
	require.Contains(t, out, `"text":"partial"`)
	for _, event := range strings.Split(out, "\n\n") {
		require.False(t, strings.HasPrefix(event, ":"), "go-genai must never receive an SSE comment event, got %q", event)
	}
}
