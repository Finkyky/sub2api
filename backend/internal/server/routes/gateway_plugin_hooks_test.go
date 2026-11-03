package routes

import (
	"context"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/service"
	contract "github.com/Wei-Shaw/sub2api/pkg/pluginapi/contracts/v1"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

type pgrSource struct {
	unbound                 bool
	protocol, kind          string
	mu                      sync.Mutex
	input, output, releases int
}

func (s *pgrSource) AcquireGroup(_ context.Context, group int64, stage contract.Stage, _ string) ([]service.PluginGatewayLease, error) {
	if s.unbound {
		return nil, nil
	}
	return []service.PluginGatewayLease{&pgrLease{source: s, group: group, stage: stage}}, nil
}

type pgrLease struct {
	source *pgrSource
	group  int64
	stage  contract.Stage
}

func (l *pgrLease) Snapshot() (*service.PluginInstanceSnapshot, error) {
	id := int64(1)
	if l.stage == contract.Output {
		id = 2
	}
	return &service.PluginInstanceSnapshot{Instance: service.PluginInstance{ID: id, Enabled: true, CurrentRevision: 1}, Revision: service.PluginInstanceRevision{Revision: 1}, Installation: service.PluginInstallation{Version: "1.0.0"}, Bindings: []service.PluginGroupBinding{{ID: id, InstanceID: id, GroupID: l.group, Stage: l.stage, Entry: "inspect", Protocol: l.source.protocol, Enabled: true}}}, nil
}
func (l *pgrLease) Release() { l.source.mu.Lock(); l.source.releases++; l.source.mu.Unlock() }
func (l *pgrLease) Check(_ context.Context, _ int64, r contract.HookRequest) (contract.HookResult, error) {
	l.source.mu.Lock()
	defer l.source.mu.Unlock()
	if r.Stage == contract.Input {
		l.source.input++
	} else {
		l.source.output++
	}
	if (l.source.kind == "input_block" && r.Stage == contract.Input) || (l.source.kind == "output_block" && r.Stage == contract.Output) {
		return contract.HookResult{Decision: contract.Block, Rejection: &contract.Rejection{Code: "deny", Message: "PRIVATE_PLUGIN_MESSAGE"}}, nil
	}
	return contract.HookResult{Decision: contract.Pass}, nil
}
func TestPluginGatewayRoutesRealChatAndBilling(t *testing.T) {
	for _, platform := range []string{service.PlatformOpenAI, service.PlatformAnthropic} {
		t.Run(platform, func(t *testing.T) {
			for _, kind := range []string{"pass", "input_block", "output_block"} {
				t.Run(kind, func(t *testing.T) {
					source := &pgrSource{protocol: service.PluginProtocolOpenAIChat, kind: kind}
					plugin := handler.NewPluginGatewayHandlerWithService(service.NewPluginGatewayServiceWithSource(source), nil)
					f := newRCRouteFixtureWithRepositories(t, platform, nil, func(f *rcRouteFixture) rcRouteRepositories {
						return rcRouteRepositories{keys: &rcRouteKeys{key: f.key}, users: f.users, accounts: f.accounts, subscriptions: f.subscriptions, billing: f.ledger, usage: f.ledger, policy: rcRoutePolicy{}, access: f.access, pluginGateway: plugin}
					})
					f.upstream.mu.Lock()
					f.upstream.responseContent = "PRIVATE_UPSTREAM_CONTENT"
					f.upstream.mu.Unlock()
					for i := 0; i < 2; i++ {
						w := f.request(t, "read_write")
						if kind == "pass" {
							requireRCRouteOK(t, w, "BYPASS")
							require.Contains(t, w.Body.String(), "PRIVATE_UPSTREAM_CONTENT")
						} else {
							require.Equal(t, 403, w.Code)
							require.NotContains(t, w.Body.String(), "PRIVATE_UPSTREAM_CONTENT")
							require.NotContains(t, w.Body.String(), "PRIVATE_PLUGIN_MESSAGE")
						}
					}
					commands, logs, quota := f.ledger.snapshot()
					expected := 2
					if kind == "input_block" {
						expected = 0
					}
					require.Equal(t, expected, f.upstream.count())
					require.Len(t, commands, expected)
					require.Len(t, logs, expected)
					if expected > 0 {
						require.Greater(t, quota, 0.0)
						access := f.access.snapshot()
						require.Len(t, access, 2)
						for i := range access {
							require.Equal(t, "BYPASS", access[i].Status)
							require.Equal(t, "plugin_bound", access[i].Reason)
							require.Equal(t, logs[i].RequestID, access[i].RequestID)
							require.Equal(t, logs[i].RequestID, commands[i].RequestID)
						}
					}
					source.mu.Lock()
					require.Equal(t, 2, source.input)
					require.Equal(t, expected, source.output)
					require.Equal(t, 4, source.releases)
					source.mu.Unlock()
				})
			}
		})
	}
}

func TestPluginGatewayRoutesLiveAliasesBoundBeforeUpgrade(t *testing.T) {
	for _, path := range []string{"/v1/live/call-1", "/backend-api/codex/call-1"} {
		for _, bound := range []bool{false, true} {
			t.Run(path+fmtBound(bound), func(t *testing.T) {
				source := &pgrSource{protocol: "unsupported.gateway", unbound: !bound}
				plugin := handler.NewPluginGatewayHandlerWithService(service.NewPluginGatewayServiceWithSource(source), nil)
				f := newRCRouteFixtureWithRepositories(t, service.PlatformOpenAI, nil, func(f *rcRouteFixture) rcRouteRepositories {
					return rcRouteRepositories{keys: &rcRouteKeys{key: f.key}, users: f.users, accounts: f.accounts, subscriptions: f.subscriptions, billing: f.ledger, usage: f.ledger, policy: rcRoutePolicy{}, access: f.access, pluginGateway: plugin}
				})
				req := httptest.NewRequest(http.MethodGet, path, nil)
				req.Header.Set("Authorization", "Bearer "+f.key.Key)
				req.Header.Set("Connection", "Upgrade")
				req.Header.Set("Upgrade", "websocket")
				w := httptest.NewRecorder()
				f.router.ServeHTTP(w, req)
				if bound {
					require.Equal(t, 422, w.Code)
					require.Contains(t, w.Body.String(), "plugin_protocol_unsupported")
				} else {
					require.Equal(t, 403, w.Code)
					require.Contains(t, w.Body.String(), "Live is not enabled")
				}
				require.Zero(t, f.upstream.count())
				require.Empty(t, w.Header().Get("Upgrade"))
			})
		}
	}
}
func fmtBound(bound bool) string {
	if bound {
		return "/bound"
	}
	return "/unbound"
}
