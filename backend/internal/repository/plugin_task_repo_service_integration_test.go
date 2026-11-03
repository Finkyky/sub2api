//go:build integration

package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	contract "github.com/Wei-Shaw/sub2api/pkg/pluginapi/contracts/v1"
	"github.com/stretchr/testify/require"
)

type ptPGExecutionBroker struct {
	prepared, committed, after, discarded atomic.Int32
	failPrepareFirst                      bool
	commitFail                            bool
}

func (b *ptPGExecutionBroker) Prepare(ctx context.Context, c *service.PluginTaskClaim, s *service.PluginInstanceSnapshot) (json.RawMessage, error) {
	count := b.prepared.Add(1)
	if b.failPrepareFirst && count == 1 {
		return nil, errors.New("private-prepare-canary")
	}
	return json.RawMessage(`{"input":{"business":"public"},"host":{}}`), nil
}
func (b *ptPGExecutionBroker) Commit(ctx context.Context, tx *sql.Tx, c *service.PluginTaskClaim, result contract.TaskResult) error {
	b.committed.Add(1)
	_, e := tx.ExecContext(ctx, `INSERT INTO sub2api_plugin_task_kv(instance_id,key,value) VALUES($1,$2,$3::jsonb) ON CONFLICT(instance_id,key) DO UPDATE SET value=EXCLUDED.value`, c.Task.InstanceID, "m5/"+c.Run.IdempotencyKey, string(result.Output))
	if e != nil {
		return e
	}
	if b.commitFail {
		return errors.New("private-commit-canary")
	}
	return nil
}
func (b *ptPGExecutionBroker) AfterCommit()                     { b.after.Add(1) }
func (b *ptPGExecutionBroker) Discard(*service.PluginTaskClaim) { b.discarded.Add(1) }
func TestPluginTaskServiceRuntimeAndSchedulingIntegration(t *testing.T) {
	ctx := context.Background()
	cfg := &config.Config{}
	cfg.Totp.EncryptionKey = strings.Repeat("d", 64)
	cipher, e := NewAESEncryptor(cfg)
	require.NoError(t, e)
	installation := piAuditRealInstallation(t, cfg)
	instanceRepo := NewPluginInstanceRepository(integrationDB)
	manager := service.NewPluginManager(NewPluginRepository(integrationDB), cipher, cfg, service.PluginHostInfo{Version: "0.1.179"}, nil)
	instances := service.NewPluginInstanceService(instanceRepo, cipher, manager, 10*time.Second)
	defer instances.Close()
	snapshot, e := instances.Create(ctx, installation.ID, "m5-runtime", json.RawMessage(`{"marker":"encrypted-result-canary"}`))
	require.NoError(t, e)
	snapshot, e = instances.Publish(ctx, snapshot.Instance.ID, snapshot.Instance.Generation, 1)
	require.NoError(t, e)
	repo := NewPluginTaskRepository(integrationDB)
	broker := &ptPGExecutionBroker{}
	tasks := service.NewPluginTaskService(repo, instances, broker, cipher)
	defer tasks.Close()
	create := func(cron string, retries int) *service.PluginTask {
		t.Helper()
		task, e := tasks.Create(ctx, &service.PluginTask{InstanceID: snapshot.Instance.ID, Name: "runtime task", Entry: "sync_prices", Cron: cron, Enabled: true, TimeoutSeconds: 30, MaxRetries: retries, RetryBackoffSeconds: 1, Input: json.RawMessage(`{"business":"public"}`)})
		require.NoError(t, e)
		return task
	}
	task := create("", 0)
	run, e := tasks.Execute(ctx, task.ID, "runtime-success")
	require.NoError(t, e)
	require.Equal(t, "succeeded", run.Status)
	require.NotNil(t, run.FinishedAt)
	require.Empty(t, run.ResultEncrypted)
	stored, e := repo.Run(ctx, run.ID)
	require.NoError(t, e)
	require.NotEmpty(t, stored.ResultEncrypted)
	require.NotContains(t, stored.ResultEncrypted, "canary")
	public, raw, e := tasks.RunSensitive(ctx, run.ID)
	require.NoError(t, e)
	require.Empty(t, public.ResultEncrypted)
	require.JSONEq(t, `{"status":"succeeded","changed":false,"output":{"marker":"encrypted-result-canary"}}`, string(raw))
	publicJSON, e := json.Marshal(run)
	require.NoError(t, e)
	require.NotContains(t, string(publicJSON), "canary")
	require.NotContains(t, string(publicJSON), "output")
	replay, e := tasks.Execute(ctx, task.ID, "runtime-success")
	require.NoError(t, e)
	require.Equal(t, run.ID, replay.ID)
	require.EqualValues(t, 1, broker.prepared.Load())
	require.EqualValues(t, 1, broker.committed.Load())
	require.EqualValues(t, 1, broker.after.Load())
	require.EqualValues(t, 1, broker.discarded.Load())
	failed, e := tasks.Execute(ctx, task.ID, "partial")
	require.NoError(t, e)
	require.Equal(t, "failed", failed.Status)
	require.False(t, failed.Changed)
	require.Equal(t, "task_failed", failed.ErrorCode)
	require.EqualValues(t, 1, broker.committed.Load())
	_, failureRaw, e := tasks.RunSensitive(ctx, failed.ID)
	require.NoError(t, e)
	require.Contains(t, string(failureRaw), `"changed":true`)
	require.Contains(t, string(failureRaw), "partial_failure")
	badBroker := &ptPGExecutionBroker{commitFail: true}
	badTasks := service.NewPluginTaskService(repo, instances, badBroker, cipher)
	defer badTasks.Close()
	failed, e = badTasks.Execute(ctx, task.ID, "rolled-back")
	require.NoError(t, e)
	require.Equal(t, "failed", failed.Status)
	require.Equal(t, "commit_failed", failed.ErrorCode)
	require.Zero(t, badBroker.after.Load())
	var count int
	require.NoError(t, integrationDB.QueryRow(`SELECT COUNT(*) FROM sub2api_plugin_task_kv WHERE instance_id=$1 AND key='m5/rolled-back'`, task.InstanceID).Scan(&count))
	require.Zero(t, count)
	t.Log("RUNTIME actual_grpc=true raw_result=AES encrypted default_DTO=result_absent replay_RPC=0 partial_commit=0 callback_rollback=true")

	// An old failed key is still due even after 101 newer independent histories.
	// This task has no cron, so a history-window scheduler would spin forever.
	boundary := create("", 1)
	old, e := repo.Claim(ctx, boundary.ID, "boundary-node", "manual", "old-due-key", time.Minute)
	require.NoError(t, e)
	oldFailed := old.Run
	oldFailed.Status = "failed"
	oldFailed.ErrorCode = "execution_failed"
	require.NoError(t, repo.Finish(ctx, old, oldFailed, nil))
	_, e = integrationDB.Exec(`UPDATE sub2api_plugin_task_runs SET retry_at=NOW()-INTERVAL '1 second' WHERE id=$1`, old.Run.ID)
	require.NoError(t, e)
	for i := 0; i < 101; i++ {
		fresh, e := repo.Claim(ctx, boundary.ID, "history-node", "manual", fmt.Sprintf("history-%03d", i), time.Minute)
		require.NoError(t, e)
		require.NoError(t, repo.Finish(ctx, fresh, ptSuccess(fresh), nil))
	}
	recent, e := repo.Runs(ctx, boundary.ID, 100)
	require.NoError(t, e)
	require.Len(t, recent, 100)
	for _, r := range recent {
		require.NotEqual(t, "old-due-key", r.IdempotencyKey)
	}
	keyReader, ok := repo.(interface {
		RetryKeys(context.Context, int64, int) ([]string, error)
	})
	require.True(t, ok, "production repository must query retry keys outside a history window")
	keys, e := keyReader.RetryKeys(ctx, boundary.ID, 1)
	require.NoError(t, e)
	require.Equal(t, []string{"old-due-key"}, keys)
	boundaryBroker := &ptPGExecutionBroker{}
	boundaryTasks := service.NewPluginTaskService(repo, instances, boundaryBroker, cipher)
	boundaryScheduler := service.NewPluginTaskScheduler(boundaryTasks)
	boundaryScheduler.Start()
	defer boundaryScheduler.Stop()
	require.Eventually(t, func() bool {
		var status string
		var attempt int
		err := integrationDB.QueryRow(`SELECT status,attempt FROM sub2api_plugin_task_runs WHERE task_id=$1 AND idempotency_key='old-due-key' ORDER BY attempt DESC LIMIT 1`, boundary.ID).Scan(&status, &attempt)
		return err == nil && status == "succeeded" && attempt == 2
	}, 8*time.Second, 50*time.Millisecond)
	boundaryScheduler.Stop()
	require.EqualValues(t, 1, boundaryBroker.prepared.Load())
	require.EqualValues(t, 1, boundaryBroker.committed.Load())
	t.Log("RETRY_HISTORY cron=empty history=101 latest100_excludes_old=true SQL_RetryKeys=old-due-key attempt=2 succeeded=true")
	scheduled := create("@every 1m", 1)
	_, e = integrationDB.Exec(`UPDATE sub2api_plugin_tasks SET next_run_at=NOW()-INTERVAL '1 minute' WHERE id=$1`, scheduled.ID)
	require.NoError(t, e)
	retryBroker := &ptPGExecutionBroker{failPrepareFirst: true}
	retryTasks := service.NewPluginTaskService(repo, instances, retryBroker, cipher)
	scheduler := service.NewPluginTaskScheduler(retryTasks)
	defer scheduler.Stop()
	scheduler.Start()
	var runs []service.PluginTaskRun
	require.Eventually(t, func() bool {
		var err error
		runs, err = repo.Runs(ctx, scheduled.ID, 10)
		return err == nil && len(runs) == 2 && runs[0].Status == "succeeded"
	}, 8*time.Second, 50*time.Millisecond)
	require.Equal(t, "scheduled", runs[1].Trigger)
	require.Equal(t, "retry", runs[0].Trigger)
	require.Equal(t, runs[1].IdempotencyKey, runs[0].IdempotencyKey)
	require.Equal(t, 1, runs[1].Attempt)
	require.Equal(t, 2, runs[0].Attempt)
	require.Equal(t, "prepare_failed", runs[1].ErrorCode)
	require.True(t, runs[0].StartedAt.Sub(*runs[1].FinishedAt) >= time.Second)
	require.EqualValues(t, 2, retryBroker.prepared.Load())
	require.EqualValues(t, 1, retryBroker.committed.Load())
	require.EqualValues(t, 1, retryBroker.after.Load())
	scheduler.Stop()
	require.EqualValues(t, 2, retryBroker.discarded.Load())
	require.NoError(t, integrationDB.QueryRow(`SELECT COUNT(*) FROM sub2api_plugin_task_kv WHERE instance_id=$1 AND key=$2`, scheduled.InstanceID, "m5/"+runs[0].IdempotencyKey).Scan(&count))
	require.Equal(t, 1, count)
	t.Log("SCHEDULER scheduled->failed->retry->succeeded same_key=true attempts=1,2 bounded_backoff>=1s business_commits=1 drained=true")
}
