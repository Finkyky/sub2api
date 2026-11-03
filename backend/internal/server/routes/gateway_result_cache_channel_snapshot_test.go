package routes

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// This fake exposes separate legacy and transaction-snapshot boundaries. Data
// changes never send an invalidation notification, reproducing a live old TTL.
type rcChannelSnapshotRepo struct {
	service.ChannelRepository
	mu                       sync.Mutex
	channels                 []service.Channel
	version, platform        string
	fail                     error
	snapshotCalls, listCalls int
}

func (r *rcChannelSnapshotRepo) copy() []service.Channel {
	b, _ := json.Marshal(r.channels)
	var result []service.Channel
	_ = json.Unmarshal(b, &result)
	return result
}
func (r *rcChannelSnapshotRepo) ListAll(context.Context) ([]service.Channel, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.listCalls++
	return r.copy(), nil
}
func (r *rcChannelSnapshotRepo) GetGroupPlatforms(context.Context, []int64) (map[int64]string, error) {
	return map[int64]string{42: r.platform}, nil
}
func (r *rcChannelSnapshotRepo) LoadResultCacheSnapshot(context.Context, int64) ([]service.Channel, map[int64]string, service.ResultCachePolicy, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.snapshotCalls++
	return r.copy(), map[int64]string{42: r.platform}, service.ResultCachePolicy{Active: true, Version: r.version, Config: service.GroupResponseResultCacheConfig{Enabled: true}}, r.fail
}
func (r *rcChannelSnapshotRepo) set(source, target, version string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.version = version
	r.channels = []service.Channel{{ID: 1, Name: "snapshot-channel", Status: service.StatusActive, GroupIDs: []int64{42}, ModelMapping: map[string]map[string]string{r.platform: {source: target}}}}
}
func TestResultCacheGatewayRouteChannelSnapshotFresh(t *testing.T) {
	for _, platform := range []string{service.PlatformOpenAI, service.PlatformAnthropic} {
		t.Run(platform, func(t *testing.T) {
			for _, mode := range []string{"read_write", "read_only", "write_only"} {
				t.Run(mode, func(t *testing.T) {
					policy := &rcRequestPolicy{version: "channel-v2"}
					repo := &rcChannelSnapshotRepo{platform: platform}
					ch := service.NewChannelService(repo, nil, nil, nil, nil)
					f := newRCRouteFixtureWithRepositories(t, platform, nil, func(f *rcRouteFixture) rcRouteRepositories {
						repo.set(f.model, f.model, "channel-v1")
						return rcRouteRepositories{keys: &rcRouteKeys{key: f.key}, users: f.users, accounts: f.accounts, subscriptions: f.subscriptions, billing: f.ledger, usage: f.ledger, policy: policy, access: f.access, channelService: ch}
					})
					old := ch.ResolveChannelMapping(context.Background(), 42, f.model)
					require.Equal(t, f.model, old.MappedModel)
					target := "gpt-5.4"
					if platform == service.PlatformAnthropic {
						target = "claude-opus-4.5"
					}
					repo.set(f.model, target, "channel-v2")
					status := "MISS"
					if mode == "write_only" {
						status = "UPDATED"
					}
					first := f.request(t, mode)
					requireRCRouteOK(t, first, status)
					require.Equal(t, target, gjson.GetBytes(first.Body.Bytes(), "model").String(), "opt-in must not adopt the old TTL channel mapping under the new authority version")
					f.upstream.mu.Lock()
					sent := gjson.GetBytes(f.upstream.bodies[0], "model").String()
					f.upstream.mu.Unlock()
					require.Equal(t, target, sent)
					nextStatus := status
					if mode == "read_write" {
						nextStatus = "HIT"
					}
					requireRCRouteOK(t, f.request(t, mode), nextStatus)
					expectedCalls := 2
					if mode == "read_write" {
						expectedCalls = 1
					}
					require.Equal(t, expectedCalls, f.upstream.count())
					repo.mu.Lock()
					snapCalls, listCalls := repo.snapshotCalls, repo.listCalls
					repo.mu.Unlock()
					require.Equal(t, 2, snapCalls, "one transaction snapshot per request, not per mapping/scheduling/billing lookup")
					require.Equal(t, 1, listCalls)
					// No request snapshot ever replaces the old shared TTL snapshot.
					require.Equal(t, f.model, ch.ResolveChannelMapping(context.Background(), 42, f.model).MappedModel)
					requireRCRouteOK(t, f.request(t, "bypass"), "BYPASS")
					f.upstream.mu.Lock()
					last := gjson.GetBytes(f.upstream.bodies[len(f.upstream.bodies)-1], "model").String()
					f.upstream.mu.Unlock()
					require.Equal(t, f.model, last)
					repo.mu.Lock()
					require.Equal(t, 2, repo.snapshotCalls)
					repo.mu.Unlock()
					commands, logs, _ := f.ledger.snapshot()
					require.Len(t, commands, expectedCalls+1)
					require.Len(t, logs, expectedCalls+1)
					access := f.access.snapshot()
					require.Len(t, access, 3)
					require.Equal(t, logs[len(logs)-1].RequestID, access[2].RequestID)
				})
			}
		})
	}
}

type rcLegacyChannelOnly struct{ service.ChannelRepository }

func TestResultCacheGatewayRouteChannelSnapshotFailure(t *testing.T) {
	for _, platform := range []string{service.PlatformOpenAI, service.PlatformAnthropic} {
		t.Run(platform, func(t *testing.T) {
			for _, kind := range []string{"error", "version", "unsupported"} {
				for _, mode := range []string{"read_write", "read_only", "write_only"} {
					t.Run(kind+"/"+mode, func(t *testing.T) {
						policy := &rcRequestPolicy{version: "channel-v2"}
						repo := &rcChannelSnapshotRepo{platform: platform}
						var source service.ChannelRepository = repo
						if kind == "unsupported" {
							source = &rcLegacyChannelOnly{ChannelRepository: repo}
						}
						ch := service.NewChannelService(source, nil, nil, nil, nil)
						f := newRCRouteFixtureWithRepositories(t, platform, nil, func(f *rcRouteFixture) rcRouteRepositories {
							repo.set(f.model, f.model, "channel-v1")
							return rcRouteRepositories{keys: &rcRouteKeys{key: f.key}, users: f.users, accounts: f.accounts, subscriptions: f.subscriptions, billing: f.ledger, usage: f.ledger, policy: policy, access: f.access, channelService: ch}
						})
						require.Equal(t, f.model, ch.ResolveChannelMapping(context.Background(), 42, f.model).MappedModel)
						target := "gpt-5.4"
						if platform == service.PlatformAnthropic {
							target = "claude-opus-4.5"
						}
						repo.set(f.model, target, "channel-v2")
						if kind != "unsupported" {
							requireRCRouteOK(t, f.request(t, "read_write"), "MISS")
						}
						before := f.upstream.count()
						reason := "semantic_snapshot_unavailable"
						repo.mu.Lock()
						switch kind {
						case "error":
							repo.fail = errors.New("snapshot read failed")
							reason = "semantic_snapshot_error"
						case "version":
							repo.version = "channel-v3"
							reason = "policy_changed"
						}
						repo.mu.Unlock()
						res := f.request(t, mode)
						requireRCRouteOK(t, res, "BYPASS")
						require.Equal(t, f.model, gjson.GetBytes(res.Body.Bytes(), "model").String(), "normal forwarding retains the old shared snapshot on freshness failure")
						require.Equal(t, before+1, f.upstream.count())
						access := f.access.snapshot()
						last := access[len(access)-1]
						require.Equal(t, reason, last.Reason)
						require.True(t, last.UpstreamCalled)
						require.Equal(t, last.RequestID, last.SourceRequestID)
						commands, logs, _ := f.ledger.snapshot()
						require.Len(t, commands, before+1)
						require.Len(t, logs, before+1)
						require.Equal(t, logs[len(logs)-1].RequestID, last.RequestID)
						repo.mu.Lock()
						repo.fail = nil
						repo.version = "channel-v2"
						repo.mu.Unlock()
						status := "HIT"
						if kind == "unsupported" {
							status = "BYPASS"
						}
						next := f.request(t, "read_write")
						requireRCRouteOK(t, next, status)
						if kind != "unsupported" {
							require.Equal(t, target, gjson.GetBytes(next.Body.Bytes(), "model").String())
							require.Equal(t, before+1, f.upstream.count(), "failed snapshot request did not overwrite the warm correct-version entry")
						}
					})
				}
			}
		})
	}
}

func TestResultCacheGatewayRouteChannelSnapshotUsagePricing(t *testing.T) {
	for _, platform := range []string{service.PlatformOpenAI, service.PlatformAnthropic} {
		t.Run(platform, func(t *testing.T) {
			for _, execution := range []string{"worker", "no_pool", "stopped", "overflow"} {
				t.Run(execution, func(t *testing.T) {
					var pool *service.UsageRecordWorkerPool
					if execution != "no_pool" {
						pool = service.NewUsageRecordWorkerPoolWithOptions(service.UsageRecordWorkerPoolOptions{WorkerCount: 1, QueueSize: 1, TaskTimeout: 10 * time.Second, OverflowPolicy: config.UsageRecordOverflowPolicySync})
						t.Cleanup(pool.Stop)
					}
					if execution == "stopped" {
						pool.Stop()
					}
					if execution == "overflow" {
						entered, release := make(chan struct{}), make(chan struct{})
						require.Equal(t, service.UsageRecordSubmitModeEnqueued, pool.Submit(func(context.Context) { close(entered); <-release }))
						<-entered
						require.Equal(t, service.UsageRecordSubmitModeEnqueued, pool.Submit(func(context.Context) {}))
						t.Cleanup(func() { close(release) })
					}
					policy := &rcRequestPolicy{version: "price-v2"}
					repo := &rcChannelSnapshotRepo{platform: platform}
					ch := service.NewChannelService(repo, nil, nil, nil, nil)
					f := newRCRouteFixtureWithRepositories(t, platform, pool, func(f *rcRouteFixture) rcRouteRepositories {
						repo.set(f.model, f.model, "price-v1")
						input, output, stats, statsOutput := 0.001, 0.002, 0.003, 0.002
						repo.channels[0].ModelPricing = []service.ChannelModelPricing{{Platform: platform, Models: []string{f.model}, InputPrice: &input, OutputPrice: &output}}
						repo.channels[0].AccountStatsPricingRules = []service.AccountStatsPricingRule{{AccountIDs: []int64{99}, Pricing: []service.ChannelModelPricing{{Platform: platform, Models: []string{f.model}, InputPrice: &stats, OutputPrice: &statsOutput}}}}
						return rcRouteRepositories{keys: &rcRouteKeys{key: f.key}, users: f.users, accounts: f.accounts, subscriptions: f.subscriptions, billing: f.ledger, usage: f.ledger, policy: policy, access: f.access, channelService: ch, channelPricingResolver: true}
					})
					require.Equal(t, 0.001, *ch.GetChannelModelPricing(context.Background(), 42, f.model).InputPrice)
					repo.mu.Lock()
					repo.version = "price-v2"
					*repo.channels[0].ModelPricing[0].InputPrice = 0.004
					*repo.channels[0].ModelPricing[0].OutputPrice = 0.005
					*repo.channels[0].AccountStatsPricingRules[0].Pricing[0].InputPrice = 0.006
					*repo.channels[0].AccountStatsPricingRules[0].Pricing[0].OutputPrice = 0.007
					repo.mu.Unlock()
					requireRCRouteOK(t, f.request(t, "write_only"), "UPDATED")
					if execution == "worker" {
						pool.Stop()
					}
					commands, logs, quota := f.ledger.snapshot()
					require.Len(t, commands, 1)
					require.Len(t, logs, 1)
					require.InDelta(t, 0.5, commands[0].BalanceCost, 1e-8, "100*0.004 + 20*0.005 must use request snapshot, not old TTL pricing")
					require.InDelta(t, 0.5, logs[0].TotalCost, 1e-8)
					require.NotNil(t, logs[0].AccountStatsCost)
					require.InDelta(t, 0.74, *logs[0].AccountStatsCost, 1e-8, "account rules must reuse the same request snapshot")
					require.InDelta(t, 0.5, commands[0].AccountQuotaCost, 1e-8, "account quota keeps the existing customer-cost basis, distinct from account stats")
					require.InDelta(t, 0.5, quota, 1e-8)
					access := f.access.snapshot()
					require.Len(t, access, 1)
					require.Equal(t, logs[0].RequestID, access[0].RequestID)
					require.Equal(t, commands[0].RequestID, logs[0].RequestID)
					repo.mu.Lock()
					require.Equal(t, 1, repo.snapshotCalls)
					require.Equal(t, 1, repo.listCalls)
					repo.mu.Unlock()
					require.Equal(t, 0.001, *ch.GetChannelModelPricing(context.Background(), 42, f.model).InputPrice)
					if execution == "overflow" {
						require.Equal(t, uint64(1), pool.Stats().SyncFallbackTasks)
					}
				})
			}
		})
	}
}
