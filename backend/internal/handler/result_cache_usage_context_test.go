package handler

import (
	"context"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

type rcUsageContextPolicy struct{}

func (rcUsageContextPolicy) Current(context.Context, int64) (service.ResultCachePolicy, error) {
	return service.ResultCachePolicy{Active: true, Version: "usage-v1", Config: service.GroupResponseResultCacheConfig{Enabled: true}}, nil
}

type rcUsageContextStore struct{ service.ResultCacheStore }
type rcUsageContextChannels struct {
	service.ChannelRepository
	calls int
}

func (r *rcUsageContextChannels) LoadResultCacheSnapshot(context.Context, int64) ([]service.Channel, map[int64]string, service.ResultCachePolicy, error) {
	r.calls++
	openai, anthropic := 2.0, 3.0
	return []service.Channel{{ID: 1, Status: service.StatusActive, GroupIDs: []int64{42}, ModelPricing: []service.ChannelModelPricing{
		{Platform: service.PlatformOpenAI, Models: []string{"m"}, InputPrice: &openai},
		{Platform: service.PlatformAnthropic, Models: []string{"m"}, InputPrice: &anthropic},
	}}}, map[int64]string{42: service.PlatformComposite}, service.ResultCachePolicy{Active: true, Version: "usage-v1", Config: service.GroupResponseResultCacheConfig{Enabled: true}}, nil
}
func TestResultCacheUsageContextPreservesSnapshotAndWorkerLifecycle(t *testing.T) {
	for _, selector := range []string{"force", "resolved"} {
		t.Run(selector, func(t *testing.T) {
			cache := service.NewResultCacheService(&rcUsageContextStore{}, rcUsageContextPolicy{}, nil, nil)
			t.Cleanup(cache.Stop)
			repo := &rcUsageContextChannels{}
			channel := service.NewChannelService(repo, nil, nil, nil, nil)
			request, cancelRequest := context.WithCancel(context.Background())
			defer cancelRequest()
			type privateKey struct{}
			request = context.WithValue(request, privateKey{}, "not transferred")
			request = context.WithValue(request, ctxkey.ClientRequestID, " client-42 ")
			request = context.WithValue(request, ctxkey.RequestID, " server-42 ")
			if selector == "force" {
				request = context.WithValue(request, ctxkey.ForcePlatform, service.PlatformAnthropic)
			} else {
				request = service.WithResolvedTargetPlatform(request, service.PlatformAnthropic)
			}
			group := int64(42)
			request = cache.BindRequestPolicy(request, service.ResultCacheIntent{Declared: true, Mode: "write_only", Scope: "user"}, &group)
			require.Equal(t, 3.0, *channel.GetChannelModelPricing(request, 42, "m").InputPrice)
			cancelRequest()
			worker, cancelWorker := context.WithTimeout(context.Background(), time.Minute)
			defer cancelWorker()
			task, abandon := wrapUsageRecordTaskContext(request, func(ctx context.Context) {
				require.NoError(t, ctx.Err(), "HTTP cancellation must not cancel billing worker")
				want, _ := worker.Deadline()
				got, ok := ctx.Deadline()
				require.True(t, ok)
				require.Equal(t, want, got)
				require.Equal(t, "client-42", ctx.Value(ctxkey.ClientRequestID))
				require.Equal(t, "server-42", ctx.Value(ctxkey.RequestID))
				require.Nil(t, ctx.Value(privateKey{}))
				require.Equal(t, 3.0, *channel.GetChannelModelPricing(ctx, 42, "m").InputPrice)
				require.Equal(t, 1, repo.calls, "reuse completed cells; never rebind or start another transaction")
				cancelWorker()
				require.ErrorIs(t, ctx.Err(), context.Canceled)
			})
			task(worker)
			abandon()
		})
	}
}
