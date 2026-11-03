//go:build integration

package repository

import (
	"context"
	"database/sql"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestResultCacheAccessPersistenceIntegration(t *testing.T) {
	ctx := context.Background()
	repo := NewResultCacheAccessRepository(integrationDB)
	var usageBefore, usageAfter int64
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM usage_logs`).Scan(&usageBefore))
	a := service.ResultCacheAccess{RequestID: uuid.NewString(), UserID: 1, APIKeyID: 2, GroupID: 3, Model: "m", Mode: "read_write", Scope: "user", Status: "HIT", SourceRequestID: "original-generation", AgeSeconds: 5, DurationMS: 10}
	require.NoError(t, repo.Append(ctx, a))
	require.NoError(t, repo.Append(ctx, a))
	var count int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM result_cache_access_logs WHERE request_id=$1`, a.RequestID).Scan(&count))
	require.Equal(t, 1, count)
	var cost sql.NullBool
	var free, called bool
	var source string
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT upstream_cost=0,cache_charge=0,upstream_called,source_request_id FROM result_cache_access_logs WHERE request_id=$1`, a.RequestID).Scan(&cost, &free, &called, &source))
	require.True(t, cost.Valid && cost.Bool)
	require.True(t, free)
	require.False(t, called)
	require.Equal(t, a.SourceRequestID, source)
	a.RequestID = uuid.NewString()
	a.Status = "MISS"
	a.UpstreamCalled = true
	require.NoError(t, repo.Append(ctx, a))
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT upstream_cost=0,cache_charge=0,upstream_called FROM result_cache_access_logs WHERE request_id=$1`, a.RequestID).Scan(&cost, &free, &called))
	require.False(t, cost.Valid, "generation cost is owned by the normal usage ledger, not invented as zero")
	require.True(t, free)
	require.True(t, called)
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM usage_logs`).Scan(&usageAfter))
	require.Equal(t, usageBefore, usageAfter)
}
func TestResultCacheAccessRetentionIntegration(t *testing.T) {
	ctx := context.Background()
	repo := NewResultCacheAccessRepository(integrationDB)
	prefix := "retention-" + uuid.NewString() + "-"
	cutoff := time.Now().UTC().Add(-7 * 24 * time.Hour)
	_, err := integrationDB.ExecContext(ctx, `INSERT INTO result_cache_access_logs(request_id,user_id,api_key_id,group_id,model,mode,scope,status,created_at) SELECT $1 || n::text,1,2,3,'m','read_write','user','HIT',$2 FROM generate_series(1,10002) n`, prefix, cutoff.Add(-time.Hour))
	require.NoError(t, err)
	for i, created := range []time.Time{cutoff, cutoff.Add(time.Hour)} {
		_, err = integrationDB.ExecContext(ctx, `INSERT INTO result_cache_access_logs(request_id,user_id,api_key_id,group_id,model,mode,scope,status,created_at) VALUES($1,1,2,3,'m','read_write','user','HIT',$2)`, prefix+[]string{"boundary", "recent"}[i], created)
		require.NoError(t, err)
	}
	count := func() int {
		var n int
		require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM result_cache_access_logs WHERE request_id LIKE $1 || '%' AND created_at<$2`, prefix, cutoff).Scan(&n))
		return n
	}
	require.NoError(t, repo.Cleanup(ctx, cutoff))
	require.Equal(t, 2, count(), "cleanup is limited to 10000 rows")
	require.NoError(t, repo.Cleanup(ctx, cutoff))
	require.Zero(t, count())
	var kept int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM result_cache_access_logs WHERE request_id LIKE $1 || '%'`, prefix).Scan(&kept))
	require.Equal(t, 2, kept, "boundary and recent records are retained")
}
