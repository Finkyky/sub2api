//go:build integration

package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type piReconcileObservedRepo struct {
	service.PluginInstanceRepository
	target  atomic.Int64
	deleted atomic.Bool
}

func (r *piReconcileObservedRepo) List(ctx context.Context, id int64) ([]service.PluginInstance, error) {
	values, err := r.PluginInstanceRepository.List(ctx, id)
	if err == nil && r.target.Load() > 0 {
		found := false
		for _, value := range values {
			if value.ID == r.target.Load() {
				found = true
			}
		}
		if !found {
			r.deleted.Store(true)
		}
	}
	return values, err
}

// Two independent node-local managers/services share only PostgreSQL authority
// and a verified installation. Every warm lease invokes a real gRPC child.
func TestPluginInstanceReconcileTwoNodesIntegration(t *testing.T) {
	ctx := context.Background()
	cfg := &config.Config{}
	cfg.Totp.EncryptionKey = strings.Repeat("b", 64)
	cipher, err := NewAESEncryptor(cfg)
	require.NoError(t, err)
	installation := piAuditRealInstallation(t, cfg)
	repo := NewPluginInstanceRepository(integrationDB)
	makeNode := func(localRepo service.PluginInstanceRepository) *service.PluginInstanceService {
		manager := service.NewPluginManager(NewPluginRepository(integrationDB), cipher, cfg, service.PluginHostInfo{Version: "0.1.179"}, nil)
		return service.NewPluginInstanceService(localRepo, cipher, manager, time.Second)
	}
	observed := &piReconcileObservedRepo{PluginInstanceRepository: repo}
	nodeA, nodeB, cold := makeNode(repo), makeNode(observed), makeNode(repo)
	defer nodeA.Close()
	defer nodeB.Close()
	defer cold.Close()
	var createdIDs []int64
	t.Cleanup(func() {
		nodeA.Close()
		nodeB.Close()
		cold.Close()
		for _, id := range createdIDs {
			snap, err := repo.Snapshot(ctx, id, 0)
			if errors.Is(err, service.ErrPluginInstanceNotFound) {
				continue
			}
			require.NoError(t, err)
			if snap.Instance.Enabled {
				snap, err = repo.Publish(ctx, id, snap.Instance.Generation, snap.Instance.CurrentRevision, false)
				require.NoError(t, err)
			}
			require.NoError(t, repo.Delete(ctx, id, snap.Instance.Generation))
		}
		plugins := NewPluginRepository(integrationDB)
		_, err := plugins.GetByID(ctx, installation.ID)
		if errors.Is(err, sql.ErrNoRows) {
			return
		}
		require.NoError(t, err)
		require.NoError(t, plugins.Delete(ctx, installation.ID, installation.BinarySHA256))
	})
	created, err := nodeA.Create(ctx, installation.ID, "reconcile-two-nodes", json.RawMessage(`{"marker":"alpha"}`))
	require.NoError(t, err)
	id := created.Instance.ID
	createdIDs = append(createdIDs, id)
	observed.target.Store(id)
	published, err := nodeA.Publish(ctx, id, created.Instance.Generation, 1)
	require.NoError(t, err)
	alpha, err := nodeB.Acquire(ctx, id)
	require.NoError(t, err)
	defer alpha.Release()
	marker := func(lease *service.PluginInstanceLease, want string) {
		t.Helper()
		result, err := lease.RunTask(ctx, "sync_prices", "reconcile-task", json.RawMessage(`{}`))
		require.NoError(t, err)
		require.JSONEq(t, `{"marker":"`+want+`"}`, string(result.Output))
	}
	marker(alpha, "alpha")
	interval, budget := 20*time.Millisecond, 2*time.Second
	require.NoError(t, nodeB.StartReconciler(ctx, interval, budget))
	require.NoError(t, cold.StartReconciler(ctx, interval, budget))
	await := func(predicate func(*service.PluginInstanceStatus) bool) {
		t.Helper()
		require.Eventually(t, func() bool {
			status, err := nodeB.Status(ctx, id)
			return err == nil && predicate(status)
		}, interval+2*budget+time.Second, 5*time.Millisecond)
	}
	localRevision := func(status *service.PluginInstanceStatus, revision int64) bool {
		for _, runtime := range status.Runtimes {
			if runtime.LocalCurrent && !runtime.Closed && runtime.Revision == revision {
				return true
			}
		}
		return false
	}
	// A saved draft changes generation, but B must restart the published alpha,
	// not beta. Its existing alpha lease remains callable while retiring.
	draft, err := nodeA.SaveConfig(ctx, id, published.Instance.Generation, json.RawMessage(`{"marker":"beta"}`))
	require.NoError(t, err)
	await(func(status *service.PluginInstanceStatus) bool {
		return len(status.Runtimes) == 2 && status.Retired && localRevision(status, 1)
	})
	unchanged, err := repo.Snapshot(ctx, id, 0)
	require.NoError(t, err)
	require.Equal(t, draft.Instance, unchanged.Instance, "reconciler must not advance DB generation")
	refreshed, err := nodeB.Acquire(ctx, id)
	require.NoError(t, err)
	defer refreshed.Release()
	marker(refreshed, "alpha")
	marker(alpha, "alpha")
	alpha.Release()
	// A publishes beta. B replaces without any Acquire on B triggering it.
	published, err = nodeA.Publish(ctx, id, draft.Instance.Generation, 2)
	require.NoError(t, err)
	await(func(status *service.PluginInstanceStatus) bool { return localRevision(status, 2) && status.Retired })
	beta, err := nodeB.Acquire(ctx, id)
	require.NoError(t, err)
	defer beta.Release()
	marker(beta, "beta")
	marker(refreshed, "alpha")
	refreshed.Release()
	// Same revision, new generation is also replaced; old beta keeps its lease.
	published, err = nodeA.Publish(ctx, id, published.Instance.Generation, 2)
	require.NoError(t, err)
	await(func(status *service.PluginInstanceStatus) bool {
		return len(status.Runtimes) == 2 && status.Retired && localRevision(status, 2)
	})
	freshBeta, err := nodeB.Acquire(ctx, id)
	require.NoError(t, err)
	defer freshBeta.Release()
	marker(beta, "beta")
	marker(freshBeta, "beta")
	beta.Release()
	status, err := cold.Status(ctx, id)
	require.NoError(t, err)
	require.False(t, status.RuntimePresent, "cold node must never prewarm")
	// Disable on A propagates to B before B performs another acquisition.
	began := time.Now()
	disabled, err := nodeA.Disable(ctx, id, published.Instance.Generation)
	require.NoError(t, err)
	await(func(status *service.PluginInstanceStatus) bool {
		return !status.Enabled && status.Retired && !localRevision(status, 2)
	})
	t.Logf("REAL_STOP_PROPAGATION elapsed=%s bound=%s", time.Since(began), interval+2*budget)
	_, err = nodeB.Acquire(ctx, id)
	require.ErrorIs(t, err, service.ErrPluginInstanceUnavailable)
	marker(freshBeta, "beta")
	// A has no local lease, so its hard delete succeeds even while B drains.
	require.NoError(t, nodeA.Delete(ctx, id, disabled.Instance.Generation))
	require.Eventually(t, observed.deleted.Load, interval+2*budget+time.Second, 5*time.Millisecond)
	_, err = nodeB.Status(ctx, id)
	require.ErrorIs(t, err, service.ErrPluginInstanceNotFound)
	marker(freshBeta, "beta")
	freshBeta.Release()
	_, err = nodeB.Acquire(ctx, id)
	require.ErrorIs(t, err, service.ErrPluginInstanceNotFound)
	nodeB.StopReconciler()
	nodeB.StopReconciler()
	cold.StopReconciler()
	require.NoError(t, nodeB.StartReconciler(ctx, interval, budget))
	nodeB.StopReconciler()
	// Shutdown releases both current and retired children; a previously held
	// lease observes closed state rather than a retained process.
	shutdown, err := nodeA.Create(ctx, installation.ID, "reconcile-shutdown", json.RawMessage(`{"marker":"shutdown"}`))
	require.NoError(t, err)
	createdIDs = append(createdIDs, shutdown.Instance.ID)
	shutdown, err = nodeA.Publish(ctx, shutdown.Instance.ID, shutdown.Instance.Generation, 1)
	require.NoError(t, err)
	lease, err := nodeB.Acquire(ctx, shutdown.Instance.ID)
	require.NoError(t, err)
	defer lease.Release()
	marker(lease, "shutdown")
	shutdown, err = nodeA.Publish(ctx, shutdown.Instance.ID, shutdown.Instance.Generation, 1)
	require.NoError(t, err)
	require.NoError(t, nodeB.Reconcile(ctx))
	replacement, err := nodeB.Acquire(ctx, shutdown.Instance.ID)
	require.NoError(t, err)
	defer replacement.Release()
	status, err = nodeB.Status(ctx, shutdown.Instance.ID)
	require.NoError(t, err)
	require.Len(t, status.Runtimes, 2)
	require.True(t, status.Retired)
	marker(replacement, "shutdown")
	nodeB.Close()
	_, err = lease.RunTask(ctx, "sync_prices", "after-close", json.RawMessage(`{}`))
	require.ErrorIs(t, err, service.ErrGeneralClosed)
	_, err = replacement.RunTask(ctx, "sync_prices", "after-close-new", json.RawMessage(`{}`))
	require.ErrorIs(t, err, service.ErrGeneralClosed)
	// Release fixture DB references too: an immutable installation with any
	// remaining instance is deliberately frozen against the next Install.
	shutdown, err = nodeA.Disable(ctx, shutdown.Instance.ID, shutdown.Instance.Generation)
	require.NoError(t, err)
	require.NoError(t, nodeA.Delete(ctx, shutdown.Instance.ID, shutdown.Instance.Generation))
	remaining, err := repo.List(ctx, installation.ID)
	require.NoError(t, err)
	require.Empty(t, remaining)
	require.NoError(t, NewPluginRepository(integrationDB).Delete(ctx, installation.ID, installation.BinarySHA256))
}
