//go:build integration

package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/stretchr/testify/require"
	"sync"
	"testing"
	"time"
)

func ptFixture(t *testing.T) (service.PluginTaskRepository, *service.PluginTask) {
	t.Helper()
	ctx := context.Background()
	p := piIntegrationInstallation(t)
	snap, err := NewPluginInstanceRepository(integrationDB).Create(ctx, p.ID, "task-instance", "cipher")
	require.NoError(t, err)
	snap, err = NewPluginInstanceRepository(integrationDB).Publish(ctx, snap.Instance.ID, snap.Instance.Generation, 1, true)
	require.NoError(t, err)
	repo := NewPluginTaskRepository(integrationDB)
	task, err := repo.Create(ctx, &service.PluginTask{InstanceID: snap.Instance.ID, Name: "task", Entry: "sync_prices", Cron: "@every 1m", Enabled: true, TimeoutSeconds: 30, MaxRetries: 2, RetryBackoffSeconds: 1, Input: json.RawMessage(`{"secret":"input-canary"}`), Scope: service.PluginTaskScope{KVKeys: []string{"scope-canary"}}})
	require.NoError(t, err)
	t.Cleanup(func() {
		current, err := repo.Get(context.Background(), task.ID)
		if errors.Is(err, service.ErrPluginTaskNotFound) {
			return
		}
		require.NoError(t, err)
		if err == nil {
			require.NoError(t, repo.Delete(context.Background(), task.ID, current.Generation))
		}
	})
	return repo, task
}
func ptSuccess(c *service.PluginTaskClaim) service.PluginTaskRun {
	r := c.Run
	r.Status = "succeeded"
	r.Changed = true
	r.ResultEncrypted = "cipher-result"
	return r
}
func TestPluginTaskRepositoryConcurrentClaimAndReplayIntegration(t *testing.T) {
	ctx := context.Background()
	repo, task := ptFixture(t)
	var wg sync.WaitGroup
	claims := make(chan *service.PluginTaskClaim, 12)
	errs := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, e := repo.Claim(ctx, task.ID, "node", "manual", "key", time.Minute)
			if e != nil {
				errs <- e
			} else {
				claims <- c
			}
		}()
	}
	wg.Wait()
	close(claims)
	close(errs)
	require.Len(t, claims, 1)
	for e := range errs {
		require.ErrorIs(t, e, service.ErrPluginTaskConflict)
	}
	c := <-claims
	_, e := repo.Claim(ctx, task.ID, "node2", "manual", "different", time.Minute)
	require.ErrorIs(t, e, service.ErrPluginTaskConflict)
	commits := 0
	require.NoError(t, repo.Finish(ctx, c, ptSuccess(c), func(tx *sql.Tx) error { commits++; return nil }))
	require.Equal(t, 1, commits)
	replay, e := repo.Claim(ctx, task.ID, "node3", "manual", "key", time.Minute)
	require.NoError(t, e)
	require.Equal(t, c.Run.ID, replay.Run.ID)
	require.Equal(t, "succeeded", replay.Run.Status)
	runs, e := repo.Runs(ctx, task.ID, 10)
	require.NoError(t, e)
	require.Len(t, runs, 1)
	t.Log("CONCURRENT valid_run=1 replay_same_run=true business_commit=1")
}
func TestPluginTaskRepositoryFencingIntegration(t *testing.T) {
	for _, kind := range []string{"expiry", "task_generation", "disabled", "instance_generation", "instance_revision", "owner", "fence", "callback_expiry"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			repo, task := ptFixture(t)
			c, e := repo.Claim(ctx, task.ID, "old-node", "manual", "old", time.Minute)
			require.NoError(t, e)
			switch kind {
			case "expiry":
				_, e = integrationDB.Exec(`UPDATE sub2api_plugin_tasks SET lease_until=NOW()-INTERVAL '1 second' WHERE id=$1`, task.ID)
			case "task_generation":
				x := *task
				x.Name = "changed"
				_, e = repo.Update(ctx, &x, task.Generation)
			case "disabled":
				x := *task
				x.Enabled = false
				_, e = repo.Update(ctx, &x, task.Generation)
			case "instance_generation":
				_, e = integrationDB.Exec(`UPDATE sub2api_plugin_instances SET generation=generation+1 WHERE id=$1`, task.InstanceID)
			case "instance_revision":
				_, e = integrationDB.Exec(`INSERT INTO sub2api_plugin_instance_revisions(instance_id,revision,config_version,config_encrypted) VALUES($1,2,1,'cipher');`, task.InstanceID)
				require.NoError(t, e)
				_, e = integrationDB.Exec(`UPDATE sub2api_plugin_instances SET latest_revision=2,current_revision=2 WHERE id=$1`, task.InstanceID)
			case "owner":
				c.Owner = "imposter"
			case "fence":
				c.Run.FencingToken++
			}
			require.NoError(t, e)
			called := false
			if kind == "callback_expiry" {
				_, e = integrationDB.Exec(`UPDATE sub2api_plugin_tasks SET lease_until=NOW()+INTERVAL '150 milliseconds' WHERE id=$1`, task.ID)
				require.NoError(t, e)
			}
			e = repo.Finish(ctx, c, ptSuccess(c), func(tx *sql.Tx) error {
				called = true
				if kind == "callback_expiry" {
					_, err := tx.Exec(`SELECT pg_sleep(0.25)`)
					return err
				}
				return nil
			})
			require.ErrorIs(t, e, service.ErrPluginTaskConflict)
			require.Equal(t, kind == "callback_expiry", called)
			saved, e := repo.Run(ctx, c.Run.ID)
			require.NoError(t, e)
			if kind != "owner" && kind != "fence" {
				require.Equal(t, "lost", saved.Status)
			}
			require.Empty(t, saved.ResultEncrypted)
		})
	}
}
func TestPluginTaskRepositoryRetryCASAuditIntegration(t *testing.T) {
	ctx := context.Background()
	repo, task := ptFixture(t)
	require.ErrorIs(t, repo.Delete(ctx, task.ID, 999), service.ErrPluginTaskConflict)
	for attempt := 1; attempt <= 3; attempt++ {
		c, e := repo.Claim(ctx, task.ID, "retry-node", "manual", "retry-key", time.Minute)
		require.NoError(t, e)
		require.Equal(t, attempt, c.Run.Attempt)
		failed := c.Run
		failed.Status = "failed"
		failed.ErrorCode = "execution_failed"
		require.NoError(t, repo.Finish(ctx, c, failed, nil))
		_, e = repo.Claim(ctx, task.ID, "retry-node", "retry", "retry-key", time.Minute)
		if attempt <= task.MaxRetries {
			require.ErrorIs(t, e, service.ErrPluginTaskConflict)
		} else {
			require.NoError(t, e)
		}
		_, e = integrationDB.Exec(`UPDATE sub2api_plugin_task_runs SET retry_at=NOW()-INTERVAL '1 second' WHERE id=$1`, c.Run.ID)
		require.NoError(t, e)
	}
	replay, e := repo.Claim(ctx, task.ID, "retry-node", "manual", "retry-key", time.Minute)
	require.NoError(t, e)
	require.Equal(t, "failed", replay.Run.Status)
	require.Equal(t, 3, replay.Run.Attempt)
	edited := *task
	edited.Input = json.RawMessage(`{"secret":"updated-canary"}`)
	edited.Scope.KVKeys = []string{"updated-scope-canary"}
	edited.Enabled = false
	updated, e := repo.Update(ctx, &edited, task.Generation)
	require.NoError(t, e)
	require.Greater(t, updated.Generation, task.Generation)
	_, e = repo.Update(ctx, &edited, task.Generation)
	require.ErrorIs(t, e, service.ErrPluginTaskConflict)
	_, e = repo.Claim(ctx, task.ID, "node", "manual", "disabled", time.Minute)
	require.ErrorIs(t, e, service.ErrPluginTaskDisabled)
	var audit string
	require.NoError(t, integrationDB.QueryRow(`SELECT string_agg(changed_fields::text,' ') FROM sub2api_plugin_task_audit WHERE task_id=$1`, task.ID).Scan(&audit))
	require.Contains(t, audit, "input")
	require.Contains(t, audit, "scope")
	require.NotContains(t, audit, "canary")
	require.NoError(t, repo.Delete(ctx, task.ID, updated.Generation))
	_, e = repo.Get(ctx, task.ID)
	require.ErrorIs(t, e, service.ErrPluginTaskNotFound)
}
func TestPluginTaskRepositoryAtomicRollbackAndDueIntegration(t *testing.T) {
	ctx := context.Background()
	repo, task := ptFixture(t)
	c, e := repo.Claim(ctx, task.ID, "node", "manual", "atomic", time.Minute)
	require.NoError(t, e)
	_, e = integrationDB.Exec(`CREATE TABLE IF NOT EXISTS m5_business_probe (task_id BIGINT PRIMARY KEY)`)
	require.NoError(t, e)
	sentinel := errors.New("commit-fail")
	e = repo.Finish(ctx, c, ptSuccess(c), func(tx *sql.Tx) error {
		_, e := tx.Exec(`INSERT INTO m5_business_probe VALUES($1)`, task.ID)
		require.NoError(t, e)
		return sentinel
	})
	require.ErrorIs(t, e, sentinel)
	var count int
	require.NoError(t, integrationDB.QueryRow(`SELECT COUNT(*) FROM m5_business_probe WHERE task_id=$1`, task.ID).Scan(&count))
	require.Zero(t, count)
	saved, e := repo.Run(ctx, c.Run.ID)
	require.NoError(t, e)
	require.Equal(t, "running", saved.Status)
	require.NoError(t, repo.Finish(ctx, c, ptSuccess(c), func(tx *sql.Tx) error { _, e := tx.Exec(`INSERT INTO m5_business_probe VALUES($1)`, task.ID); return e }))
	_, e = integrationDB.Exec(`UPDATE sub2api_plugin_tasks SET next_run_at=NOW()-INTERVAL '1 minute' WHERE id=$1`, task.ID)
	require.NoError(t, e)
	ids, e := repo.Due(ctx, 1)
	require.NoError(t, e)
	require.Len(t, ids, 1)
	require.Equal(t, task.ID, ids[0])
	ids, e = repo.Due(ctx, 0)
	require.NoError(t, e)
	require.LessOrEqual(t, len(ids), 100)
	tx, e := integrationDB.Begin()
	require.NoError(t, e)
	defer tx.Rollback()
	b, e := migrations.FS.ReadFile("254_general_plugin_tasks.sql")
	require.NoError(t, e)
	for i := 0; i < 2; i++ {
		_, e = tx.Exec(string(b))
		require.NoError(t, e)
	}
}

func TestPluginTaskRepositoryTakeoverAndFailedCallbackIntegration(t *testing.T) {
	ctx := context.Background()
	repo, task := ptFixture(t)
	old, e := repo.Claim(ctx, task.ID, "node-old", "manual", "takeover", time.Minute)
	require.NoError(t, e)
	_, e = integrationDB.Exec(`UPDATE sub2api_plugin_tasks SET lease_until=NOW()-INTERVAL '1 second' WHERE id=$1`, task.ID)
	require.NoError(t, e)
	_, e = repo.Due(ctx, 1)
	require.NoError(t, e)
	lost, e := repo.Run(ctx, old.Run.ID)
	require.NoError(t, e)
	require.Equal(t, "lost", lost.Status)
	_, e = integrationDB.Exec(`UPDATE sub2api_plugin_task_runs SET retry_at=NOW()-INTERVAL '1 second' WHERE id=$1`, old.Run.ID)
	require.NoError(t, e)
	fresh, e := repo.Claim(ctx, task.ID, "node-new", "retry", "takeover", time.Minute)
	require.NoError(t, e)
	require.Greater(t, fresh.Run.FencingToken, old.Run.FencingToken)
	require.Equal(t, 2, fresh.Run.Attempt)
	called := false
	e = repo.Finish(ctx, old, ptSuccess(old), func(*sql.Tx) error { called = true; return nil })
	require.ErrorIs(t, e, service.ErrPluginTaskConflict)
	require.False(t, called)
	failed := fresh.Run
	failed.Status = "failed"
	failed.ErrorCode = "execution_failed"
	failed.Changed = true
	require.NoError(t, repo.Finish(ctx, fresh, failed, func(*sql.Tx) error { called = true; return nil }))
	require.False(t, called)
	saved, e := repo.Run(ctx, fresh.Run.ID)
	require.NoError(t, e)
	require.False(t, saved.Changed)
	require.NotNil(t, saved.FinishedAt)
	require.GreaterOrEqual(t, saved.DurationMS, int64(0))
	t.Log("TAKEOVER fence_increased=true old_commit_rejected=true stale=lost failed_callback=false")
}
func TestPluginTaskRepositoryReplayDuringAnotherRunIntegration(t *testing.T) {
	ctx := context.Background()
	repo, task := ptFixture(t)
	done, e := repo.Claim(ctx, task.ID, "node", "manual", "done", time.Minute)
	require.NoError(t, e)
	require.NoError(t, repo.Finish(ctx, done, ptSuccess(done), nil))
	active, e := repo.Claim(ctx, task.ID, "node2", "manual", "active", time.Minute)
	require.NoError(t, e)
	replay, e := repo.Claim(ctx, task.ID, "node3", "manual", "done", time.Minute)
	require.NoError(t, e)
	require.Equal(t, done.Run.ID, replay.Run.ID)
	require.Equal(t, "succeeded", replay.Run.Status)
	require.NoError(t, repo.Finish(ctx, active, ptSuccess(active), nil))
}
func TestPluginTaskRepositoryDBClockAfterLockWaitIntegration(t *testing.T) {
	ctx := context.Background()
	repo, task := ptFixture(t)
	claim, e := repo.Claim(ctx, task.ID, "clock-node", "manual", "clock", 150*time.Millisecond)
	require.NoError(t, e)
	blocker, e := integrationDB.Begin()
	require.NoError(t, e)
	defer blocker.Rollback()
	_, e = blocker.Exec(`SELECT id FROM sub2api_plugin_instances WHERE id=$1 FOR UPDATE`, task.InstanceID)
	require.NoError(t, e)
	result := make(chan error, 1)
	var callback sync.Once
	called := false
	go func() {
		result <- repo.Finish(ctx, claim, ptSuccess(claim), func(*sql.Tx) error { callback.Do(func() { called = true }); return nil })
	}()
	time.Sleep(250 * time.Millisecond)
	require.NoError(t, blocker.Commit())
	require.ErrorIs(t, <-result, service.ErrPluginTaskConflict)
	require.False(t, called)
	saved, e := repo.Run(ctx, claim.Run.ID)
	require.NoError(t, e)
	require.Equal(t, "lost", saved.Status)
	t.Log("DB_CLOCK waited_past_lease=true callback=false run=lost")
}

func TestPluginTaskRepositoryCallbackAuthorityMutationIntegration(t *testing.T) {
	ctx := context.Background()
	repo, task := ptFixture(t)
	claim, e := repo.Claim(ctx, task.ID, "node", "manual", "self-mutation", time.Minute)
	require.NoError(t, e)
	e = repo.Finish(ctx, claim, ptSuccess(claim), func(tx *sql.Tx) error {
		_, err := tx.Exec(`UPDATE sub2api_plugin_instances SET generation=generation+1 WHERE id=$1`, task.InstanceID)
		return err
	})
	require.ErrorIs(t, e, service.ErrPluginTaskConflict)
	saved, e := repo.Run(ctx, claim.Run.ID)
	require.NoError(t, e)
	require.Equal(t, "running", saved.Status)
	require.Empty(t, saved.ResultEncrypted)
	require.NoError(t, repo.Finish(ctx, claim, ptSuccess(claim), nil))
}
