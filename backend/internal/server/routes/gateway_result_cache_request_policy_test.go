package routes

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// The audit callback is a deterministic barrier: the authoritative version
// changes after the request has entered its semantic preprocessing path.
type rcRequestPolicy struct {
	mu      sync.Mutex
	version string
	calls   int
	err     error
}

func (p *rcRequestPolicy) Current(context.Context, int64) (service.ResultCachePolicy, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	return service.ResultCachePolicy{Active: true, Version: p.version, Config: service.GroupResponseResultCacheConfig{Enabled: true, TTLSeconds: 60, MaxResponseBytes: 2 << 20, Revision: 1}}, p.err
}
func (p *rcRequestPolicy) set(v string) { p.mu.Lock(); defer p.mu.Unlock(); p.version = v }
func rcRequestPolicyFixture(t *testing.T, platform string, p *rcRequestPolicy) *rcRouteFixture {
	return newRCRouteFixtureWithRepositories(t, platform, nil, func(f *rcRouteFixture) rcRouteRepositories {
		return rcRouteRepositories{keys: &rcRouteKeys{key: f.key}, users: f.users, accounts: f.accounts, subscriptions: f.subscriptions, billing: f.ledger, usage: f.ledger, policy: p, access: f.access}
	})
}
func rcRequestAuditHook(f *rcRouteFixture, hook func()) {
	f.audit.mu.Lock()
	defer f.audit.mu.Unlock()
	f.audit.onCheck = hook
}

func TestResultCacheGatewayRouteRequestPolicyTransition(t *testing.T) {
	for _, platform := range []string{service.PlatformOpenAI, service.PlatformAnthropic} {
		t.Run(platform, func(t *testing.T) {
			for _, mode := range []string{"read_write", "read_only", "write_only"} {
				for _, warm := range []bool{false, true} {
					name := mode + "/cold"
					if warm {
						name = mode + "/warm"
					}
					t.Run(name, func(t *testing.T) {
						p := &rcRequestPolicy{version: "request-v2"}
						f := rcRequestPolicyFixture(t, platform, p)
						if warm {
							requireRCRouteOK(t, f.request(t, "read_write"), "MISS")
						}
						before := f.upstream.count()
						p.set("request-v1")
						rcRequestAuditHook(f, func() { p.set("request-v2") })
						res := f.request(t, mode)
						requireRCRouteOK(t, res, "BYPASS")
						require.Equal(t, before+1, f.upstream.count(), "version transition must not replay the warm new-version entry")
						access := f.access.snapshot()
						last := access[len(access)-1]
						require.Equal(t, "policy_changed", last.Reason)
						require.True(t, last.UpstreamCalled)
						require.Equal(t, last.RequestID, last.SourceRequestID, "source is this billed upstream request, never a warm cached generation")
						commands, logs, _ := f.ledger.snapshot()
						require.Len(t, commands, before+1, "normal upstream execution still incurs normal billing")
						require.Len(t, logs, before+1)
						require.Equal(t, logs[len(logs)-1].RequestID, last.RequestID)
						rcRequestAuditHook(f, nil)
						expected := "MISS"
						if warm {
							expected = "HIT"
						}
						requireRCRouteOK(t, f.request(t, "read_write"), expected)
						if !warm {
							requireRCRouteOK(t, f.request(t, "read_write"), "HIT")
						}
					})
				}
			}
		})
	}
}

func TestResultCacheGatewayRouteRequestPolicyEarlyFailure(t *testing.T) {
	for _, platform := range []string{service.PlatformOpenAI, service.PlatformAnthropic} {
		t.Run(platform, func(t *testing.T) {
			p := &rcRequestPolicy{version: "request-v1", err: errors.New("authority temporarily unavailable")}
			f := rcRequestPolicyFixture(t, platform, p)
			rcRequestAuditHook(f, func() { p.mu.Lock(); defer p.mu.Unlock(); p.err = nil })
			requireRCRouteOK(t, f.request(t, "read_write"), "BYPASS")
			access := f.access.snapshot()
			require.Len(t, access, 1)
			require.Equal(t, "policy_error", access[0].Reason)
			require.True(t, access[0].UpstreamCalled)
			require.Equal(t, access[0].RequestID, access[0].SourceRequestID)
			commands, logs, _ := f.ledger.snapshot()
			require.Len(t, commands, 1)
			require.Len(t, logs, 1)
			rcRequestAuditHook(f, nil)
			requireRCRouteOK(t, f.request(t, "read_write"), "MISS")
			requireRCRouteOK(t, f.request(t, "read_write"), "HIT")
			require.Equal(t, 2, f.upstream.count())
		})
	}
}

func TestResultCacheGatewayRouteRequestPolicyOptOut(t *testing.T) {
	for _, platform := range []string{service.PlatformOpenAI, service.PlatformAnthropic} {
		t.Run(platform, func(t *testing.T) {
			p := &rcRequestPolicy{version: "request-v1"}
			f := rcRequestPolicyFixture(t, platform, p)
			// No gateway_cache payload or protocol headers means zero result policy reads.
			body := strings.ReplaceAll(`{"model":"gpt-5.2","messages":[{"role":"user","content":"fixture question"}],"stream":false}`, "gpt-5.2", f.model)
			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+f.key.Key)
			rec := httptest.NewRecorder()
			f.router.ServeHTTP(rec, req)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			require.Empty(t, rec.Header().Get("X-Gateway-Cache-Status"))
			p.mu.Lock()
			calls := p.calls
			p.mu.Unlock()
			require.Zero(t, calls)
			requireRCRouteOK(t, f.request(t, "bypass"), "BYPASS")
			p.mu.Lock()
			calls = p.calls
			p.mu.Unlock()
			require.Equal(t, 1, calls, "explicit bypass keeps only Begin's existing policy read")
			require.Equal(t, 2, f.upstream.count())
			commands, logs, _ := f.ledger.snapshot()
			require.Len(t, commands, 2)
			require.Len(t, logs, 2)
		})
	}
}

type rcRequestCompositeRepo struct {
	service.CompositeModelRouteRepository
	p               *rcRequestPolicy
	platform, model string
	transition      bool
}

func (r *rcRequestCompositeRepo) ListByGroup(_ context.Context, gid int64, _ bool) ([]service.CompositeModelRoute, error) {
	routes := []service.CompositeModelRoute{{ID: 1, GroupID: gid, PublicModel: "public-alias", MatchType: service.CompositeRouteMatchExact, TargetPlatform: r.platform, UpstreamModel: r.model, Endpoint: service.CompositeRouteEndpointAny, Enabled: true}}
	if r.transition {
		r.p.set("request-v2")
	}
	return routes, nil
}
func rcRequestAt(t *testing.T, f *rcRouteFixture, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(strings.ReplaceAll(fmt.Sprintf(rcRouteRequest, "read_write"), "gpt-5.2", "public-alias")))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+f.key.Key)
	req.Header.Set("X-Gateway-Cache-Protocol", "result-cache")
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	return rec
}
func TestResultCacheGatewayRouteRequestPolicyCompositeTransition(t *testing.T) {
	for _, platform := range []string{service.PlatformOpenAI, service.PlatformAnthropic} {
		t.Run(platform, func(t *testing.T) {
			for _, path := range []string{"/v1/chat/completions", "/chat/completions"} {
				for _, warm := range []bool{false, true} {
					name := path + "/cold"
					if warm {
						name = path + "/warm"
					}
					t.Run(name, func(t *testing.T) {
						p := &rcRequestPolicy{version: "request-v2"}
						repo := &rcRequestCompositeRepo{p: p, platform: platform}
						f := newRCRouteFixtureWithRepositories(t, platform, nil, func(f *rcRouteFixture) rcRouteRepositories {
							f.key.Group.Platform = service.PlatformComposite
							repo.model = f.model
							return rcRouteRepositories{keys: &rcRouteKeys{key: f.key}, users: f.users, accounts: f.accounts, subscriptions: f.subscriptions, billing: f.ledger, usage: f.ledger, policy: p, access: f.access, compositeResolver: service.NewCompositeRouteResolver(repo)}
						})
						if warm {
							requireRCRouteOK(t, rcRequestAt(t, f, path), "MISS")
						}
						before := f.upstream.count()
						p.set("request-v1")
						repo.transition = true
						res := rcRequestAt(t, f, path)
						requireRCRouteOK(t, res, "BYPASS")
						require.Equal(t, before+1, f.upstream.count())
						access := f.access.snapshot()
						last := access[len(access)-1]
						require.Equal(t, "policy_changed", last.Reason)
						require.True(t, last.UpstreamCalled)
						require.Equal(t, last.RequestID, last.SourceRequestID)
						commands, logs, _ := f.ledger.snapshot()
						require.Len(t, commands, before+1)
						require.Len(t, logs, before+1)
						repo.transition = false
						expected := "MISS"
						if warm {
							expected = "HIT"
						}
						requireRCRouteOK(t, rcRequestAt(t, f, path), expected)
					})
				}
			}
		})
	}
}
