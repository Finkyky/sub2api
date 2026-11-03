package repository

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/codexclient"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestCodexLinuxDisabledPreservesLegacyIdentity(t *testing.T) {
	upstream := NewHTTPUpstream(&config.Config{}).(*httpUpstreamService)
	request, err := http.NewRequest(http.MethodGet, "https://chatgpt.com/backend-api/codex/responses", nil)
	require.NoError(t, err)
	request.Header.Set("originator", "legacy")
	request.Header.Set("User-Agent", "legacy-agent")
	request.Header.Set("Accept-Encoding", "gzip")
	want := request.Header.Clone()
	require.Equal(t, service.HTTPUpstreamProfileOpenAI, upstream.resolveCodexClientProfile(request, service.HTTPUpstreamProfileOpenAI))
	require.Equal(t, want, request.Header)
}

func TestCodexLinuxDoWithTLSPreservesFixedProfilePrecedence(t *testing.T) {
	proxy := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusBadGateway)
	}))
	defer proxy.Close()
	cfg := &config.Config{}
	cfg.Gateway.OpenAICodexClientProfile = codexclient.Profile
	upstream := NewHTTPUpstream(cfg).(*httpUpstreamService)
	request, err := http.NewRequestWithContext(service.WithHTTPUpstreamProfile(t.Context(), service.HTTPUpstreamProfileOpenAI), http.MethodGet, "https://chatgpt.com/backend-api/codex/responses", nil)
	require.NoError(t, err)
	request.Header.Set("originator", "legacy")
	_, err = upstream.DoWithTLS(request, proxy.URL, 1, 1, &tlsfingerprint.Profile{Name: "must-not-override-codex"})
	require.ErrorContains(t, err, "502")
	require.Equal(t, codexclient.UserAgent, request.Header.Get("User-Agent"))
	require.Len(t, upstream.clients, 1)
	for _, entry := range upstream.clients {
		t.Cleanup(entry.client.CloseIdleConnections)
		require.Equal(t, upstreamProtocolModeCodexLinux, entry.protocolMode)
	}
}

func TestCodexLinuxTransportProfileScope(t *testing.T) {
	cfg := &config.Config{}
	cfg.Gateway.OpenAICodexClientProfile = codexclient.Profile
	upstream := NewHTTPUpstream(cfg).(*httpUpstreamService)
	for _, test := range []struct {
		name, target string
		originator   bool
		input, want  service.HTTPUpstreamProfile
	}{
		{"oauth", "https://chatgpt.com/backend-api/codex/responses", true, service.HTTPUpstreamProfileOpenAI, service.HTTPUpstreamProfileCodexLinux},
		{"harvest", "https://chatgpt.com/backend-api/codex/responses", true, service.HTTPUpstreamProfileOpenAIHarvest, service.HTTPUpstreamProfileCodexLinuxHarvest},
		{"api_key", "https://api.openai.com/v1/responses", true, service.HTTPUpstreamProfileOpenAI, service.HTTPUpstreamProfileOpenAI},
		{"other_host", "https://example.com/backend-api/codex/responses", true, service.HTTPUpstreamProfileOpenAI, service.HTTPUpstreamProfileOpenAI},
		{"other_path", "https://chatgpt.com/backend-api/other", true, service.HTTPUpstreamProfileOpenAI, service.HTTPUpstreamProfileOpenAI},
		{"no_identity", "https://chatgpt.com/backend-api/codex/responses", false, service.HTTPUpstreamProfileOpenAI, service.HTTPUpstreamProfileOpenAI},
		{"other_provider", "https://chatgpt.com/backend-api/codex/responses", true, service.HTTPUpstreamProfileGrok, service.HTTPUpstreamProfileGrok},
	} {
		t.Run(test.name, func(t *testing.T) {
			request, err := http.NewRequest(http.MethodPost, test.target, nil)
			require.NoError(t, err)
			request.Header.Set("User-Agent", "original")
			if test.originator {
				request.Header.Set("originator", "original")
			}
			require.Equal(t, test.want, upstream.resolveCodexClientProfile(request, test.input))
			if test.want == test.input {
				require.Equal(t, "original", request.Header.Get("User-Agent"))
			} else {
				require.Equal(t, codexclient.UserAgent, request.Header.Get("User-Agent"))
			}
		})
	}
}

func TestCodexLinuxTransportKeepsPoolsSeparateAndHarvestNoReuse(t *testing.T) {
	upstream := NewHTTPUpstream(&config.Config{}).(*httpUpstreamService)
	normal, err := upstream.acquireClientWithProfile("", 1, 3, service.HTTPUpstreamProfileOpenAI)
	require.NoError(t, err)
	aligned, err := upstream.acquireClientWithProfile("", 1, 3, service.HTTPUpstreamProfileCodexLinux)
	require.NoError(t, err)
	harvest, err := upstream.acquireClientWithProfile("", 1, 3, service.HTTPUpstreamProfileCodexLinuxHarvest)
	require.NoError(t, err)
	require.NotSame(t, normal.client, aligned.client)
	require.NotSame(t, aligned.client, harvest.client)
	for _, entry := range []*upstreamClientEntry{normal, aligned, harvest} {
		t.Cleanup(entry.client.CloseIdleConnections)
	}
	transport := aligned.client.Transport.(*http.Transport)
	require.False(t, transport.ForceAttemptHTTP2)
	require.True(t, transport.DisableCompression)
	require.NotNil(t, transport.DialTLSContext)
	require.False(t, transport.DisableKeepAlives)
	require.True(t, harvest.client.Transport.(*http.Transport).DisableKeepAlives)
}
