package service

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/codexclient"
	alignedws "github.com/Wei-Shaw/sub2api/internal/pkg/codexwebsocket"
	coderws "github.com/coder/websocket"
	"github.com/stretchr/testify/require"
)

func TestCodexAlignedWSAdapterErrorCompatibility(t *testing.T) {
	closed := normalizeCodexWSError(fmt.Errorf("wrapped: %w", alignedws.CloseError{Code: alignedws.StatusPolicyViolation, Reason: "test policy"}))
	require.Equal(t, coderws.StatusPolicyViolation, coderws.CloseStatus(closed))
	var converted coderws.CloseError
	require.ErrorAs(t, closed, &converted)
	require.Equal(t, "test policy", converted.Reason)
	require.ErrorIs(t, normalizeCodexWSError(context.Canceled), context.Canceled)
	require.NoError(t, normalizeCodexWSError(nil))
	require.ErrorIs(t, normalizeCodexWSError(fmt.Errorf("read: %w", alignedws.ErrMessageTooBig)), coderws.ErrMessageTooBig)
}

func TestCodexAlignedWSAdapterHandlesPingAndClose(t *testing.T) {
	SetCodexClientProfile(codexclient.Profile)
	t.Cleanup(func() { SetCodexClientProfile("") })
	finished := make(chan error, 1)
	proxy := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		connection, err := coderws.Accept(writer, request, &coderws.AcceptOptions{CompressionMode: coderws.CompressionContextTakeover})
		if err != nil {
			finished <- err
			return
		}
		defer connection.CloseNow()
		ctx, cancel := context.WithTimeout(request.Context(), 5*time.Second)
		defer cancel()
		connection.CloseRead(ctx)
		if err := connection.Ping(ctx); err != nil {
			finished <- err
			return
		}
		if err := connection.Write(ctx, coderws.MessageText, []byte(`{"ok":true}`)); err != nil {
			finished <- err
			return
		}
		finished <- connection.Close(coderws.StatusPolicyViolation, "test policy")
	}))
	defer proxy.Close()
	connection, _, _, err := newDefaultOpenAIWSClientDialer().Dial(t.Context(), "ws://chatgpt.com/backend-api/codex/responses", nil, proxy.URL)
	require.NoError(t, err)
	aligned, ok := connection.(*codexAlignedWSClientConn)
	require.True(t, ok)
	defer aligned.CloseNow()
	require.True(t, aligned.RequiresReaderLoop())
	require.False(t, aligned.SupportsIdlePingWithoutReader())
	payload, err := connection.ReadMessage(t.Context())
	require.NoError(t, err)
	require.JSONEq(t, `{"ok":true}`, string(payload))
	require.Equal(t, int64(1), aligned.UpstreamPingCount())
	_, err = connection.ReadMessage(t.Context())
	require.Equal(t, coderws.StatusPolicyViolation, coderws.CloseStatus(err))
	require.NoError(t, <-finished)
}

func TestCodexAlignedWSAdapterLeavesOtherTargetsUnchanged(t *testing.T) {
	SetCodexClientProfile(codexclient.Profile)
	t.Cleanup(func() { SetCodexClientProfile("") })
	proxy := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		connection, err := coderws.Accept(writer, request, nil)
		if err != nil {
			return
		}
		defer connection.CloseNow()
		_, _, _ = connection.Read(request.Context())
	}))
	defer proxy.Close()
	for _, target := range []string{"ws://example.com/backend-api/codex/responses", "ws://chatgpt.com/other", "ws://api.openai.com/v1/responses"} {
		connection, _, _, err := newDefaultOpenAIWSClientDialer().Dial(t.Context(), target, http.Header{"User-Agent": {"original"}}, proxy.URL)
		require.NoError(t, err)
		legacy, ok := connection.(*coderOpenAIWSClientConn)
		require.True(t, ok)
		require.NoError(t, legacy.CloseNow())
	}
}
