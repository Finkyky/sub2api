package service

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/codexclient"
	coderws "github.com/coder/websocket"
	"github.com/stretchr/testify/require"
)

func TestCodexLinuxProfileOverridesPanelAccountAndHarvestIdentity(t *testing.T) {
	SetCodexClientProfile(codexclient.Profile)
	t.Cleanup(func() { SetCodexClientProfile("") })
	SetCodexCanonicalUserAgentResolver(func() string { return "codex_vscode/9.9.9 (Mac OS X; arm64)" })
	t.Cleanup(func() { SetCodexCanonicalUserAgentResolver(nil) })
	for _, harvest := range []bool{false, true} {
		headers := http.Header{"Originator": {"codex_vscode"}, "User-Agent": {"third-party"}, "Version": {"9.9.9"}}
		if harvest {
			applyOpenAICodexTicketHarvestIdentity(headers, "gpt-6-astra")
		} else {
			enforceCodexIdentityHeadersWithUA(headers, "codex-tui/1.0.0 (Windows; arm64)")
		}
		require.Equal(t, "codex_exec", headers.Get("originator"))
		require.Equal(t, "0.153.4", headers.Get("version"))
		require.Equal(t, "codex_exec/0.153.4 (Alpine Linux 3.22.1; x86_64) xterm-256color (codex_exec; 0.153.4)", headers.Get("user-agent"))
	}
}

func TestCodexLinuxSettingsExposePinnedEffectiveIdentity(t *testing.T) {
	cfg := &config.Config{}
	cfg.Gateway.OpenAICodexClientProfile = codexclient.Profile
	settings := &SettingService{cfg: cfg}
	require.Equal(t, codexclient.UserAgent, settings.GetOpenAICodexUserAgent(t.Context()))
	require.Equal(t, codexclient.Version, settings.GetOpenAICodexClientVersion(t.Context()))
	require.Equal(t, "0.153.4", codexclient.Version,
		"legacy TLS profile version is backed by the 0.153.4 wire capture")
	require.NotEqual(t, codexCLIVersion, codexclient.Version,
		"legacy TLS profile must stay opt-in and must not replace the current default identity")
}

func TestCodexLinuxProfileCanReturnToLegacyIdentity(t *testing.T) {
	SetCodexClientProfile("")
	t.Cleanup(func() { SetCodexClientProfile("") })
	legacy := resolveCodexOutboundIdentity("")
	SetCodexClientProfile(codexclient.Profile)
	require.Equal(t, codexclient.UserAgent, resolveCodexOutboundIdentity("").userAgent)
	SetCodexClientProfile("")
	require.Equal(t, legacy, resolveCodexOutboundIdentity(""))
}

func TestCodexLinuxWebSocketFinalIdentityMatchesCapture(t *testing.T) {
	SetCodexClientProfile(codexclient.Profile)
	t.Cleanup(func() { SetCodexClientProfile("") })
	fixtureBytes, err := os.ReadFile("../pkg/codexclient/testdata/linux-0.153.4-websocket.json")
	require.NoError(t, err)
	var fixture struct {
		Headers map[string]string `json:"headers"`
	}
	require.NoError(t, json.Unmarshal(fixtureBytes, &fixture))
	captured := make(chan http.Header, 1)
	proxy := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		captured <- request.Header.Clone()
		connection, err := coderws.Accept(writer, request, nil)
		if err != nil {
			return
		}
		defer connection.CloseNow()
		_, _, _ = connection.Read(request.Context())
	}))
	defer proxy.Close()
	dialer := newDefaultOpenAIWSClientDialer()
	headers := http.Header{
		"User-Agent": {"untrusted-client"}, "Originator": {"other"}, "Version": {"0.1.0"},
		"Openai-Beta": {"responses_websockets=2026-02-06"},
		"Session-Id":  {"account-specific-session"}, "Authorization": {"Bearer offline-dummy-key"},
	}
	connection, _, _, err := dialer.Dial(t.Context(), "ws://chatgpt.com/backend-api/codex/responses", headers, proxy.URL)
	require.NoError(t, err)
	defer connection.Close()
	select {
	case actual := <-captured:
		for _, name := range []string{"user-agent", "originator", "openai-beta", "upgrade", "sec-websocket-version"} {
			require.Equal(t, fixture.Headers[name], actual.Get(name), name)
		}
		require.Equal(t, codexclient.Version, actual.Get("version"))
		require.Equal(t, "account-specific-session", actual.Get("session-id"))
		require.Equal(t, "Bearer offline-dummy-key", actual.Get("authorization"))
		require.Equal(t, fixture.Headers["sec-websocket-extensions"], actual.Get("sec-websocket-extensions"))
	case <-time.After(3 * time.Second):
		t.Fatal("no proxy handshake captured")
	}
	require.Equal(t, "untrusted-client", headers.Get("User-Agent"))
}

func TestCodexLinuxWebSocketClientProfileIsolatesPools(t *testing.T) {
	dialer := newDefaultOpenAIWSClientDialer().(*coderOpenAIWSClientDialer)
	legacy, err := dialer.proxyHTTPClientForProfile("http://127.0.0.1:8080", false)
	require.NoError(t, err)
	aligned, err := dialer.proxyHTTPClientForProfile("http://127.0.0.1:8080", true)
	require.NoError(t, err)
	direct, err := dialer.proxyHTTPClientForProfile("", true)
	require.NoError(t, err)
	require.NotSame(t, legacy, aligned)
	require.NotSame(t, aligned, direct)
	reused, err := dialer.proxyHTTPClientForProfile("", true)
	require.NoError(t, err)
	require.Same(t, direct, reused)
	for _, client := range []*http.Client{legacy, aligned, direct} {
		t.Cleanup(client.CloseIdleConnections)
	}
	for _, client := range []*http.Client{aligned, direct} {
		transport := client.Transport.(*http.Transport)
		require.NotNil(t, transport.DialTLSContext)
		require.False(t, transport.ForceAttemptHTTP2)
	}
}
