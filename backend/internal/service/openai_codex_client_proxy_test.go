package service

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/codexclient"
	"github.com/stretchr/testify/require"
)

func TestCodexLinuxWebSocketRejectsRedirects(t *testing.T) {
	SetCodexClientProfile(codexclient.Profile)
	t.Cleanup(func() { SetCodexClientProfile("") })
	var requests atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		if request.URL.Hostname() == "chatgpt.com" {
			writer.Header().Set("Location", "http://redirect.invalid/not-codex")
			writer.WriteHeader(http.StatusFound)
			return
		}
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer proxy.Close()
	dialer := newDefaultOpenAIWSClientDialer()
	_, status, _, err := dialer.Dial(t.Context(), "ws://chatgpt.com/backend-api/codex/responses", nil, proxy.URL)
	require.Error(t, err)
	require.Equal(t, http.StatusFound, status)
	require.Equal(t, int32(1), requests.Load())
}

func TestCodexLinuxWebSocketPreservesEnvironmentProxy(t *testing.T) {
	SetCodexClientProfile(codexclient.Profile)
	t.Cleanup(func() { SetCodexClientProfile("") })
	connected := make(chan string, 1)
	proxy := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		connected <- request.Method
		writer.WriteHeader(http.StatusBadGateway)
	}))
	defer proxy.Close()
	t.Setenv("HTTPS_PROXY", proxy.URL)
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	t.Setenv("NO_PROXY", "")
	t.Setenv("no_proxy", "")
	dialer := newDefaultOpenAIWSClientDialer()
	_, _, _, err := dialer.Dial(t.Context(), "wss://chatgpt.com/backend-api/codex/responses", nil, "")
	require.ErrorContains(t, err, "502")
	select {
	case method := <-connected:
		require.Equal(t, http.MethodConnect, method)
	case <-time.After(time.Second):
		t.Fatal("environment proxy was bypassed")
	}
}

func TestCodexLinuxWebSocketProxyPrecedenceAndNoProxy(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:8080")
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:8081")
	t.Setenv("no_proxy", "")
	t.Setenv("NO_PROXY", "chatgpt.com")
	target, err := url.Parse("wss://chatgpt.com/backend-api/codex/responses")
	require.NoError(t, err)
	selected, err := resolveCodexWebSocketProxy(target, "")
	require.NoError(t, err)
	require.Empty(t, selected)
	selected, err = resolveCodexWebSocketProxy(target, "socks5h://127.0.0.1:1080")
	require.NoError(t, err)
	require.Equal(t, "socks5h://127.0.0.1:1080", selected)
	t.Setenv("NO_PROXY", "")
	selected, err = resolveCodexWebSocketProxy(target, "")
	require.NoError(t, err)
	require.Equal(t, "http://127.0.0.1:8080", selected)
	target.Scheme = "ws"
	selected, err = resolveCodexWebSocketProxy(target, "")
	require.NoError(t, err)
	require.Equal(t, "http://127.0.0.1:8081", selected)
}
