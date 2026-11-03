//go:build integration

package repository

import (
	"context"
	"fmt"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"sync"
	"testing"
	"time"
)

func rcIntegrationGroup(t *testing.T, repo *groupRepository) *service.Group {
	t.Helper()
	g := rcWriteGroup()
	g.ID = 0
	g.Name = fmt.Sprintf("result-cache-%d", time.Now().UnixNano())
	g.ResponseResultCacheConfig = service.GroupResponseResultCacheConfig{Enabled: true, TTLSeconds: 67, MaxResponseBytes: 8192, AllowGroupSharing: true, Revision: 99}
	require.NoError(t, repo.Create(context.Background(), g))
	return g
}
func TestGroupResultCacheRoundTripIntegration(t *testing.T) {
	tx := testEntTx(t)
	repo := newGroupRepositoryWithSQL(tx.Client(), tx)
	ctx := context.Background()
	g := rcIntegrationGroup(t, repo)
	require.EqualValues(t, 1, g.ResponseResultCacheConfig.Revision)
	for _, read := range []func(context.Context, int64) (*service.Group, error){repo.GetByID, repo.GetByIDLite} {
		loaded, err := read(ctx, g.ID)
		require.NoError(t, err)
		require.Equal(t, g.ResponseResultCacheConfig, loaded.ResponseResultCacheConfig)
	}
	groups, _, err := repo.ListWithFilters(ctx, pagination.PaginationParams{Page: 1, PageSize: 10}, service.PlatformOpenAI, service.StatusActive, g.Name, nil)
	require.NoError(t, err)
	require.Len(t, groups, 1)
	require.Equal(t, g.ResponseResultCacheConfig, groups[0].ResponseResultCacheConfig)
	copy := *g
	copy.ID = 0
	copy.Name += "-copy"
	copy.ResponseResultCacheConfig.Revision = 55
	require.NoError(t, repo.CreateFromSource(ctx, &copy, g.ID))
	require.EqualValues(t, 1, copy.ResponseResultCacheConfig.Revision)
	expected := g.ResponseResultCacheConfig
	expected.Revision = 1
	require.Equal(t, expected, copy.ResponseResultCacheConfig)
}
func TestGroupResultCacheRevisionCASIntegration(t *testing.T) {
	repo := newGroupRepositoryWithSQL(testEntClient(t), integrationDB)
	ctx := context.Background()
	g := rcIntegrationGroup(t, repo)
	a, err := repo.GetByID(ctx, g.ID)
	require.NoError(t, err)
	b, err := repo.GetByID(ctx, g.ID)
	require.NoError(t, err)
	a.ResponseResultCacheConfig.TTLSeconds = 45
	b.ResponseResultCacheConfig.TTLSeconds = 60
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, snapshot := range []*service.Group{a, b} {
		wg.Add(1)
		go func(snapshot *service.Group) { defer wg.Done(); <-start; results <- repo.Update(ctx, snapshot) }(snapshot)
	}
	close(start)
	wg.Wait()
	close(results)
	success, conflict := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else {
			require.ErrorIs(t, err, service.ErrResultCachePolicyConflict)
			conflict++
		}
	}
	require.Equal(t, 1, success)
	require.Equal(t, 1, conflict)
	loaded, err := repo.GetByID(ctx, g.ID)
	require.NoError(t, err)
	require.EqualValues(t, 2, loaded.ResponseResultCacheConfig.Revision)
	var stale *service.Group
	if a.ResponseResultCacheConfig.Revision == 1 {
		stale = a
	} else {
		stale = b
	}
	require.EqualValues(t, 1, stale.ResponseResultCacheConfig.Revision)
	require.ErrorIs(t, repo.Update(ctx, stale), service.ErrResultCachePolicyConflict)
	require.EqualValues(t, 1, stale.ResponseResultCacheConfig.Revision)
	current, err := repo.GetByID(ctx, g.ID)
	require.NoError(t, err)
	require.Equal(t, loaded.ResponseResultCacheConfig, current.ResponseResultCacheConfig)
}
func TestGroupResultCacheDisablePolicyIntegration(t *testing.T) {
	repo := newGroupRepositoryWithSQL(testEntClient(t), integrationDB)
	ctx := context.Background()
	g := rcIntegrationGroup(t, repo)
	policies := NewResultCachePolicyRepository(integrationDB)
	before, err := policies.Current(ctx, g.ID)
	require.NoError(t, err)
	require.True(t, before.Config.Enabled)
	g.ResponseResultCacheConfig.Enabled = false
	require.NoError(t, repo.Update(ctx, g))
	after, err := policies.Current(ctx, g.ID)
	require.NoError(t, err)
	require.False(t, after.Config.Enabled)
	require.NotEqual(t, before.Version, after.Version)
	require.EqualValues(t, 2, after.Config.Revision)
}
