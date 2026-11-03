//go:build unit

package service

import (
	"context"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
)

func TestResolveUsageBillingRequestID_ForcedWebSearchBeatsClientID(t *testing.T) {
	t.Parallel()
	ctx := context.WithValue(context.Background(), ctxkey.ClientRequestID, "client-shared-id")
	got := resolveUsageBillingRequestID(ctx, "web_search:uuid-1")
	require.Equal(t, "web_search:uuid-1", got)
}

func TestResolveUsageBillingRequestID_ClientWinsOverPlainUpstream(t *testing.T) {
	t.Parallel()
	ctx := context.WithValue(context.Background(), ctxkey.ClientRequestID, "client-shared-id")
	got := resolveUsageBillingRequestID(ctx, "resp_abc")
	require.Equal(t, "client:client-shared-id", got)
}

func TestIsForcedUsageBillingRequestID(t *testing.T) {
	t.Parallel()
	require.True(t, isForcedUsageBillingRequestID("web_search:x"))
	require.True(t, isForcedUsageBillingRequestID("grok-video:task-1"))
	require.True(t, isForcedUsageBillingRequestID("grok_audio:up-1"))
	require.True(t, isForcedUsageBillingRequestID("grok_realtime:sess-1"))
	require.True(t, isForcedUsageBillingRequestID(GeminiCachedContentCreateUsageRequestID("abc")))
	require.False(t, isForcedUsageBillingRequestID("resp_abc"))
}

func TestGeminiCachedContentUsageRequestIDs(t *testing.T) {
	t.Parallel()
	publicID, err := NewGeminiCachedContentPublicID()
	require.NoError(t, err)
	create := GeminiCachedContentCreateUsageRequestID(publicID)
	patch := GeminiCachedContentPatchUsageRequestID(publicID)
	require.Equal(t, "gcache:create:"+publicID, create)
	require.True(t, strings.HasPrefix(patch, "gcache:patch:"+publicID+":"))
	require.NotEqual(t, patch, GeminiCachedContentPatchUsageRequestID(publicID), "每次延长各自唯一")
	require.LessOrEqual(t, len(create), 64, "usage_logs.request_id 列长 64")
	require.LessOrEqual(t, len(patch), 64, "usage_logs.request_id 列长 64")

	ctx := context.WithValue(context.Background(), ctxkey.ClientRequestID, "client-shared-id")
	require.Equal(t, create, resolveUsageBillingRequestID(ctx, create), "缓存费用行不被客户端请求 id 覆盖")
	require.Equal(t, patch, resolveUsageBillingRequestID(ctx, patch))
}

func TestStableGrokAudioBillingRequestID(t *testing.T) {
	t.Parallel()
	require.Equal(t, "grok_audio:up-1", StableGrokAudioBillingRequestID("up-1"))
	require.Equal(t, "grok_audio:up-1", StableGrokAudioBillingRequestID("grok_audio:up-1"))
	got := StableGrokAudioBillingRequestID("")
	require.True(t, strings.HasPrefix(got, "grok_audio:"))
	require.Greater(t, len(got), len("grok_audio:"))
}

func TestStableGrokRealtimeBillingRequestID(t *testing.T) {
	t.Parallel()
	require.Equal(t, "grok_realtime:s1", StableGrokRealtimeBillingRequestID("s1"))
	require.Equal(t, "grok_realtime:s1", StableGrokRealtimeBillingRequestID("grok_realtime:s1"))
	got := StableGrokRealtimeBillingRequestID("")
	require.True(t, strings.HasPrefix(got, "grok_realtime:"))
}

func TestResolveUsageBillingRequestID_ForcedGrokAudioBeatsClientID(t *testing.T) {
	t.Parallel()
	ctx := context.WithValue(context.Background(), ctxkey.ClientRequestID, "client-shared-id")
	got := resolveUsageBillingRequestID(ctx, StableGrokAudioBillingRequestID("up-9"))
	require.Equal(t, "grok_audio:up-9", got)
}

func TestResolveUsageBillingRequestID_Schema64Boundaries(t *testing.T) {
	t.Parallel()
	for _, ns := range []struct {
		name    string
		key     ctxkey.Key
		prefix  string
		lengths []int
	}{{"client", ctxkey.ClientRequestID, "client:", []int{57, 58, 64}}, {"local", ctxkey.RequestID, "local:", []int{58, 59, 64}}} {
		for _, n := range ns.lengths {
			t.Run(ns.name+"/"+strings.Repeat("x", n), func(t *testing.T) {
				raw := strings.Repeat("x", n)
				ctx := context.WithValue(context.Background(), ns.key, raw)
				id := resolveUsageBillingRequestID(ctx, "upstream")
				require.LessOrEqual(t, len(id), 64)
				require.True(t, strings.HasPrefix(id, ns.prefix))
				require.Equal(t, id, resolveUsageBillingRequestID(ctx, "other-upstream"))
				if len(ns.prefix)+n <= 64 {
					require.Equal(t, ns.prefix+raw, id)
				} else {
					require.Contains(t, id, "sha256:")
					ctx2 := context.WithValue(context.Background(), ns.key, raw[:n-1]+"y")
					require.NotEqual(t, id, resolveUsageBillingRequestID(ctx2, "upstream"))
				}
			})
		}
	}
	for _, prefix := range []string{"", "web_search:", "grok-video:", "grok_audio:", "grok_realtime:"} {
		raw := prefix + strings.Repeat("x", 100)
		id := resolveUsageBillingRequestID(context.Background(), raw)
		require.LessOrEqual(t, len(id), 64)
		require.True(t, strings.HasPrefix(id, prefix))
		require.Equal(t, id, resolveUsageBillingRequestID(context.Background(), raw))
	}
}
