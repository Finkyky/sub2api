package repository

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type pluginTaskRepository struct{ db *sql.DB }

func NewPluginTaskRepository(db *sql.DB) service.PluginTaskRepository {
	return &pluginTaskRepository{db: db}
}

var _ service.PluginTaskRepository = (*pluginTaskRepository)(nil)

const pluginTaskColumns = `id,instance_id,name,entry,cron,enabled,timeout_seconds,max_retries,retry_backoff_seconds,generation,input,scope,next_run_at`
const pluginTaskRunColumns = `id,task_id,instance_id,config_revision,task_generation,trigger,idempotency_key,attempt,fencing_token,status,changed,error_code,started_at,finished_at,duration_ms,result_encrypted`

type pluginTaskAuthority struct {
	task  service.PluginTask
	fence int64
	owner sql.NullString
	until sql.NullTime
}

func compactPluginTaskInput(input []byte) json.RawMessage {
	var compact bytes.Buffer
	if json.Compact(&compact, input) != nil {
		return append(json.RawMessage(nil), input...)
	}
	return append(json.RawMessage(nil), compact.Bytes()...)
}
func scanPluginTask(row pluginScanner) (*service.PluginTask, error) {
	var t service.PluginTask
	var input, scope []byte
	err := row.Scan(&t.ID, &t.InstanceID, &t.Name, &t.Entry, &t.Cron, &t.Enabled, &t.TimeoutSeconds, &t.MaxRetries, &t.RetryBackoffSeconds, &t.Generation, &input, &scope, &t.NextRunAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrPluginTaskNotFound
	}
	if err != nil {
		return nil, err
	}
	t.Input = compactPluginTaskInput(input)
	if err = json.Unmarshal(scope, &t.Scope); err != nil {
		return nil, err
	}
	return &t, nil
}
func scanPluginTaskRun(row pluginScanner) (*service.PluginTaskRun, error) {
	var r service.PluginTaskRun
	err := row.Scan(&r.ID, &r.TaskID, &r.InstanceID, &r.ConfigRevision, &r.TaskGeneration, &r.Trigger, &r.IdempotencyKey, &r.Attempt, &r.FencingToken, &r.Status, &r.Changed, &r.ErrorCode, &r.StartedAt, &r.FinishedAt, &r.DurationMS, &r.ResultEncrypted)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrPluginTaskNotFound
	}
	return &r, err
}

// All writers lock instance before task. This also serializes with instance
// publication/deletion, including its cascade to task/run rows.
func lockPluginTask(ctx context.Context, tx *sql.Tx, id int64) (*pluginTaskAuthority, service.PluginInstance, error) {
	var instanceID int64
	if e := tx.QueryRowContext(ctx, `SELECT instance_id FROM sub2api_plugin_tasks WHERE id=$1`, id).Scan(&instanceID); e != nil {
		if errors.Is(e, sql.ErrNoRows) {
			e = service.ErrPluginTaskNotFound
		}
		return nil, service.PluginInstance{}, e
	}
	instance, e := readPluginInstance(ctx, tx, instanceID, true)
	if e != nil {
		return nil, instance, e
	}
	var a pluginTaskAuthority
	var input, scope []byte
	t := &a.task
	e = tx.QueryRowContext(ctx, `SELECT `+pluginTaskColumns+`,fencing_token,lease_owner,lease_until FROM sub2api_plugin_tasks WHERE id=$1 FOR UPDATE`, id).Scan(&t.ID, &t.InstanceID, &t.Name, &t.Entry, &t.Cron, &t.Enabled, &t.TimeoutSeconds, &t.MaxRetries, &t.RetryBackoffSeconds, &t.Generation, &input, &scope, &t.NextRunAt, &a.fence, &a.owner, &a.until)
	if errors.Is(e, sql.ErrNoRows) {
		e = service.ErrPluginTaskNotFound
	}
	if e != nil {
		return nil, instance, e
	}
	t.Input = compactPluginTaskInput(input)
	e = json.Unmarshal(scope, &t.Scope)
	return &a, instance, e
}

// NOW() is transaction-start time in PostgreSQL and becomes stale while waiting
// for a row lock or a callback. Sample the database wall clock after each wait.
func pluginTaskDBNow(ctx context.Context, tx *sql.Tx) (time.Time, error) {
	var now time.Time
	e := tx.QueryRowContext(ctx, `SELECT clock_timestamp()`).Scan(&now)
	return now.UTC(), e
}
func pluginTaskAudit(ctx context.Context, tx *sql.Tx, t *service.PluginTask, action string, fields []string) error {
	b, e := json.Marshal(fields)
	if e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO sub2api_plugin_task_audit(task_id,instance_id,generation,action,changed_fields) VALUES($1,$2,$3,$4,$5::jsonb)`, t.ID, t.InstanceID, t.Generation, action, string(b))
	return e
}
func (r *pluginTaskRepository) List(ctx context.Context, instanceID int64) ([]service.PluginTask, error) {
	if instanceID < 0 {
		return nil, service.ErrPluginTaskInvalid
	}
	q := `SELECT ` + pluginTaskColumns + ` FROM sub2api_plugin_tasks`
	args := []any{}
	if instanceID > 0 {
		q += ` WHERE instance_id=$1`
		args = append(args, instanceID)
	}
	q += ` ORDER BY id`
	rows, e := r.db.QueryContext(ctx, q, args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	tasks := make([]service.PluginTask, 0)
	for rows.Next() {
		t, e := scanPluginTask(rows)
		if e != nil {
			return nil, e
		}
		tasks = append(tasks, *t)
	}
	return tasks, rows.Err()
}
func (r *pluginTaskRepository) Get(ctx context.Context, id int64) (*service.PluginTask, error) {
	if id <= 0 {
		return nil, service.ErrPluginTaskInvalid
	}
	return scanPluginTask(r.db.QueryRowContext(ctx, `SELECT `+pluginTaskColumns+` FROM sub2api_plugin_tasks WHERE id=$1`, id))
}
func pluginTaskNormalize(t *service.PluginTask) (*service.PluginTask, error) {
	if t == nil {
		return nil, service.ErrPluginTaskInvalid
	}
	data, e := json.Marshal(t)
	if e != nil {
		return nil, service.ErrPluginTaskInvalid
	}
	var copy service.PluginTask
	if e = json.Unmarshal(data, &copy); e != nil {
		return nil, service.ErrPluginTaskInvalid
	}
	copy.Name = strings.TrimSpace(copy.Name)
	copy.Cron = strings.TrimSpace(copy.Cron)
	if e = service.ValidatePluginTaskDefinition(copy); e != nil {
		return nil, e
	}
	return &copy, nil
}
func pluginTaskNext(t *service.PluginTask, now time.Time) error {
	t.NextRunAt = nil
	if t.Enabled && t.Cron != "" {
		next, e := service.PluginTaskNextRun(t.Cron, now)
		if e != nil {
			return e
		}
		t.NextRunAt = &next
	}
	return nil
}
func (r *pluginTaskRepository) Create(ctx context.Context, input *service.PluginTask) (*service.PluginTask, error) {
	t, e := pluginTaskNormalize(input)
	if e != nil {
		return nil, e
	}
	if t.ID != 0 {
		return nil, service.ErrPluginTaskInvalid
	}
	tx, e := r.db.BeginTx(ctx, nil)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	instance, e := readPluginInstance(ctx, tx, t.InstanceID, true)
	if e != nil {
		return nil, e
	}
	snap, e := readPluginInstanceSnapshot(ctx, tx, instance, 0)
	if e != nil {
		return nil, e
	}
	if e = service.ValidatePluginTaskEntry(*t, snap.Installation.Manifest); e != nil {
		return nil, e
	}
	now, e := pluginTaskDBNow(ctx, tx)
	if e != nil {
		return nil, e
	}
	if e = pluginTaskNext(t, now); e != nil {
		return nil, e
	}
	scope, e := json.Marshal(t.Scope)
	if e != nil {
		return nil, e
	}
	e = tx.QueryRowContext(ctx, `INSERT INTO sub2api_plugin_tasks(instance_id,name,entry,cron,enabled,timeout_seconds,max_retries,retry_backoff_seconds,input,scope,next_run_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9::jsonb,$10::jsonb,$11) RETURNING id,generation`, t.InstanceID, t.Name, t.Entry, t.Cron, t.Enabled, t.TimeoutSeconds, t.MaxRetries, t.RetryBackoffSeconds, string(t.Input), string(scope), t.NextRunAt).Scan(&t.ID, &t.Generation)
	if e != nil {
		return nil, e
	}
	if e = pluginTaskAudit(ctx, tx, t, "create", []string{"name", "entry", "cron", "enabled", "timeout_seconds", "max_retries", "retry_backoff_seconds", "input", "scope"}); e != nil {
		return nil, e
	}
	return t, tx.Commit()
}
func pluginTaskChangedFields(old, next service.PluginTask) []string {
	fields := make([]string, 0)
	for _, pair := range []struct {
		name string
		a, b any
	}{
		{"name", old.Name, next.Name}, {"entry", old.Entry, next.Entry}, {"cron", old.Cron, next.Cron}, {"enabled", old.Enabled, next.Enabled}, {"timeout_seconds", old.TimeoutSeconds, next.TimeoutSeconds}, {"max_retries", old.MaxRetries, next.MaxRetries}, {"retry_backoff_seconds", old.RetryBackoffSeconds, next.RetryBackoffSeconds}, {"input", old.Input, next.Input}, {"scope", old.Scope, next.Scope},
	} {
		if !reflect.DeepEqual(pair.a, pair.b) {
			fields = append(fields, pair.name)
		}
	}
	return fields
}
func (r *pluginTaskRepository) Update(ctx context.Context, input *service.PluginTask, expected int64) (*service.PluginTask, error) {
	t, e := pluginTaskNormalize(input)
	if e != nil {
		return nil, e
	}
	if t.ID <= 0 || expected <= 0 {
		return nil, service.ErrPluginTaskInvalid
	}
	tx, e := r.db.BeginTx(ctx, nil)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	a, instance, e := lockPluginTask(ctx, tx, t.ID)
	if e != nil {
		return nil, e
	}
	if a.task.Generation != expected {
		return nil, service.ErrPluginTaskConflict
	}
	if a.task.InstanceID != t.InstanceID {
		return nil, service.ErrPluginTaskInvalid
	}
	snap, e := readPluginInstanceSnapshot(ctx, tx, instance, 0)
	if e != nil {
		return nil, e
	}
	if e = service.ValidatePluginTaskEntry(*t, snap.Installation.Manifest); e != nil {
		return nil, e
	}
	now, e := pluginTaskDBNow(ctx, tx)
	if e != nil {
		return nil, e
	}
	fields := pluginTaskChangedFields(a.task, *t)
	t.Generation = expected + 1
	t.NextRunAt = a.task.NextRunAt
	if t.Cron != a.task.Cron || t.Enabled != a.task.Enabled {
		if e = pluginTaskNext(t, now); e != nil {
			return nil, e
		}
	}
	scope, e := json.Marshal(t.Scope)
	if e != nil {
		return nil, e
	}
	_, e = tx.ExecContext(ctx, `UPDATE sub2api_plugin_tasks SET name=$2,entry=$3,cron=$4,enabled=$5,timeout_seconds=$6,max_retries=$7,retry_backoff_seconds=$8,generation=$9,input=$10::jsonb,scope=$11::jsonb,next_run_at=$12,updated_at=$13 WHERE id=$1`, t.ID, t.Name, t.Entry, t.Cron, t.Enabled, t.TimeoutSeconds, t.MaxRetries, t.RetryBackoffSeconds, t.Generation, string(t.Input), string(scope), t.NextRunAt, now)
	if e != nil {
		return nil, e
	}
	a.task = *t
	if e = pluginTaskReap(ctx, tx, a, instance, now); e != nil {
		return nil, e
	}
	if e = pluginTaskAudit(ctx, tx, t, "update", fields); e != nil {
		return nil, e
	}
	return t, tx.Commit()
}
func (r *pluginTaskRepository) Delete(ctx context.Context, id, expected int64) error {
	if id <= 0 || expected <= 0 {
		return service.ErrPluginTaskInvalid
	}
	tx, e := r.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	a, _, e := lockPluginTask(ctx, tx, id)
	if e != nil {
		return e
	}
	if a.task.Generation != expected {
		return service.ErrPluginTaskConflict
	}
	if e = pluginTaskAudit(ctx, tx, &a.task, "delete", []string{}); e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, `DELETE FROM sub2api_plugin_tasks WHERE id=$1`, id)
	if e != nil {
		return e
	}
	return tx.Commit()
}
func pluginTaskRetryDelay(t service.PluginTask, attempt int) time.Duration {
	seconds := int64(t.RetryBackoffSeconds)
	for i := 1; i < attempt && seconds < 3600; i++ {
		seconds *= 2
	}
	if seconds > 3600 {
		seconds = 3600
	}
	return time.Duration(seconds) * time.Second
}

// Reap only the actual running row and preserve terminal history. Generation
// changes revoke its business authority even if the wall-clock lease is live.
func pluginTaskReap(ctx context.Context, tx *sql.Tx, a *pluginTaskAuthority, instance service.PluginInstance, now time.Time) error {
	var runID, taskGen, instanceGen, revision, fence int64
	var attempt int
	e := tx.QueryRowContext(ctx, `SELECT id,task_generation,instance_generation,config_revision,fencing_token,attempt FROM sub2api_plugin_task_runs WHERE task_id=$1 AND status='running' FOR UPDATE`, a.task.ID).Scan(&runID, &taskGen, &instanceGen, &revision, &fence, &attempt)
	if errors.Is(e, sql.ErrNoRows) {
		return nil
	}
	if e != nil {
		return e
	}
	valid := a.task.Enabled && instance.Enabled && a.until.Valid && a.until.Time.After(now) && a.owner.Valid && a.fence == fence && a.task.Generation == taskGen && instance.Generation == instanceGen && instance.CurrentRevision == revision
	if valid {
		return nil
	}
	var retry any
	if attempt <= a.task.MaxRetries {
		retry = now.Add(pluginTaskRetryDelay(a.task, attempt))
	}
	_, e = tx.ExecContext(ctx, `UPDATE sub2api_plugin_task_runs SET status='lost',changed=FALSE,error_code='lease_lost',result_encrypted='',finished_at=$2,duration_ms=GREATEST(0,(EXTRACT(EPOCH FROM ($2::timestamptz-started_at))*1000)::bigint),retry_at=$3 WHERE id=$1`, runID, now, retry)
	if e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, `UPDATE sub2api_plugin_tasks SET lease_owner=NULL,lease_until=NULL WHERE id=$1 AND fencing_token=$2`, a.task.ID, fence)
	if e == nil && a.fence == fence {
		a.owner = sql.NullString{}
		a.until = sql.NullTime{}
	}
	return e
}
func taskRepositoryKeyValid(key string) bool {
	return strings.TrimSpace(key) != "" && len(key) <= 256 && !strings.ContainsAny(key, "\x00\r\n")
}
func (r *pluginTaskRepository) Claim(ctx context.Context, id int64, owner, trigger, key string, ttl time.Duration) (*service.PluginTaskClaim, error) {
	if id <= 0 || !taskRepositoryKeyValid(owner) || len(owner) > 160 || !taskRepositoryKeyValid(key) || ttl <= 0 || ttl > 10*time.Minute || (trigger != "manual" && trigger != "scheduled" && trigger != "retry") {
		return nil, service.ErrPluginTaskInvalid
	}
	tx, e := r.db.BeginTx(ctx, nil)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	a, instance, e := lockPluginTask(ctx, tx, id)
	if e != nil {
		return nil, e
	}
	now, e := pluginTaskDBNow(ctx, tx)
	if e != nil {
		return nil, e
	}
	if e = pluginTaskReap(ctx, tx, a, instance, now); e != nil {
		return nil, e
	}
	// Commit any stale-run transition even when this acquisition is rejected.
	reject := func(err error) (*service.PluginTaskClaim, error) {
		if e := tx.Commit(); e != nil {
			return nil, e
		}
		return nil, err
	}
	if !a.task.Enabled || !instance.Enabled {
		return reject(service.ErrPluginTaskDisabled)
	}
	latest, e := scanPluginTaskRun(tx.QueryRowContext(ctx, `SELECT `+pluginTaskRunColumns+` FROM sub2api_plugin_task_runs WHERE task_id=$1 AND idempotency_key=$2 ORDER BY attempt DESC LIMIT 1`, id, key))
	if e != nil && !errors.Is(e, service.ErrPluginTaskNotFound) {
		return nil, e
	}
	attempt := 1
	if latest != nil && e == nil {
		var instanceGen int64
		var retryAt sql.NullTime
		e = tx.QueryRowContext(ctx, `SELECT instance_generation,retry_at FROM sub2api_plugin_task_runs WHERE id=$1`, latest.ID).Scan(&instanceGen, &retryAt)
		if e != nil {
			return nil, e
		}
		if latest.Status == "succeeded" || latest.Attempt > a.task.MaxRetries || latest.TaskGeneration != a.task.Generation || instanceGen != instance.Generation || latest.ConfigRevision != instance.CurrentRevision {
			c := &service.PluginTaskClaim{Task: a.task, Run: *latest, InstanceGeneration: instanceGen}
			return c, tx.Commit()
		}
		if !retryAt.Valid || retryAt.Time.After(now) {
			return reject(service.ErrPluginTaskConflict)
		}
		attempt = latest.Attempt + 1
		trigger = "retry"
	} else if trigger == "retry" {
		return reject(service.ErrPluginTaskConflict)
	}
	var active bool
	e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sub2api_plugin_task_runs WHERE task_id=$1 AND status='running')`, id).Scan(&active)
	if e != nil {
		return nil, e
	}
	if active || (a.until.Valid && a.until.Time.After(now)) {
		return reject(service.ErrPluginTaskConflict)
	}
	if trigger == "scheduled" {
		if a.task.Cron == "" || a.task.NextRunAt == nil || a.task.NextRunAt.After(now) || key != service.PluginTaskScheduledKey(a.task) {
			return reject(service.ErrPluginTaskConflict)
		}
		if e = pluginTaskNext(&a.task, now); e != nil {
			return nil, e
		}
	}
	// Publication cannot change while these instance/task locks are held.
	snap, e := readPluginInstanceSnapshot(ctx, tx, instance, 0)
	if e != nil {
		return nil, e
	}
	if e = service.ValidatePluginTaskEntry(a.task, snap.Installation.Manifest); e != nil {
		return reject(e)
	}
	a.fence++
	until := now.Add(ttl)
	a.owner = sql.NullString{String: owner, Valid: true}
	a.until = sql.NullTime{Time: until, Valid: true}
	_, e = tx.ExecContext(ctx, `UPDATE sub2api_plugin_tasks SET fencing_token=$2,lease_owner=$3,lease_until=$4,next_run_at=$5 WHERE id=$1`, id, a.fence, owner, until, a.task.NextRunAt)
	if e != nil {
		return nil, e
	}
	run, e := scanPluginTaskRun(tx.QueryRowContext(ctx, `INSERT INTO sub2api_plugin_task_runs(task_id,instance_id,instance_generation,config_revision,task_generation,trigger,idempotency_key,attempt,fencing_token,status,started_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,'running',$10) RETURNING `+pluginTaskRunColumns, id, instance.ID, instance.Generation, instance.CurrentRevision, a.task.Generation, trigger, key, attempt, a.fence, now))
	if e != nil {
		return nil, e
	}
	claim := &service.PluginTaskClaim{Task: a.task, Run: *run, Owner: owner, LeaseUntil: until, InstanceGeneration: instance.Generation}
	return claim, tx.Commit()
}
func samePluginTaskRun(a, b service.PluginTaskRun) bool {
	return a.ID == b.ID && a.TaskID == b.TaskID && a.InstanceID == b.InstanceID && a.ConfigRevision == b.ConfigRevision && a.TaskGeneration == b.TaskGeneration && a.Trigger == b.Trigger && a.IdempotencyKey == b.IdempotencyKey && a.Attempt == b.Attempt && a.FencingToken == b.FencingToken
}
func pluginTaskClaimValid(a *pluginTaskAuthority, i service.PluginInstance, c *service.PluginTaskClaim, stored *service.PluginTaskRun, now time.Time) bool {
	return c != nil && stored.Status == "running" && samePluginTaskRun(*stored, c.Run) && a.task.ID == c.Task.ID && a.task.InstanceID == c.Task.InstanceID && stored.TaskID == a.task.ID && stored.InstanceID == i.ID && a.task.Generation == stored.TaskGeneration && c.Task.Generation == stored.TaskGeneration && a.task.Entry == c.Task.Entry && reflect.DeepEqual(a.task.Input, c.Task.Input) && reflect.DeepEqual(a.task.Scope, c.Task.Scope) && a.task.Enabled && i.Enabled && i.Generation == c.InstanceGeneration && i.CurrentRevision == stored.ConfigRevision && a.fence == stored.FencingToken && a.owner.Valid && a.owner.String == c.Owner && a.until.Valid && a.until.Time.After(now)
}
func (r *pluginTaskRepository) reapClaim(ctx context.Context, c *service.PluginTaskClaim) error {
	tx, e := r.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	a, i, e := lockPluginTask(ctx, tx, c.Task.ID)
	if errors.Is(e, service.ErrPluginTaskNotFound) || errors.Is(e, service.ErrPluginInstanceNotFound) {
		return nil
	}
	if e != nil {
		return e
	}
	// A forged owner/fence must never revoke another node's valid lease.
	if a.fence != c.Run.FencingToken || !a.owner.Valid || a.owner.String != c.Owner {
		return nil
	}
	now, e := pluginTaskDBNow(ctx, tx)
	if e != nil {
		return e
	}
	if e = pluginTaskReap(ctx, tx, a, i, now); e != nil {
		return e
	}
	return tx.Commit()
}
func (r *pluginTaskRepository) Finish(ctx context.Context, c *service.PluginTaskClaim, run service.PluginTaskRun, callback func(*sql.Tx) error) error {
	if c == nil || c.Task.ID <= 0 || !samePluginTaskRun(c.Run, run) || (run.Status != "succeeded" && run.Status != "failed") || len(run.ResultEncrypted) > 2*1024*1024 || len(run.ErrorCode) > 64 || (run.Status == "succeeded" && run.ErrorCode != "") {
		return service.ErrPluginTaskInvalid
	}
	tx, e := r.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	a, instance, e := lockPluginTask(ctx, tx, c.Task.ID)
	if errors.Is(e, service.ErrPluginTaskNotFound) || errors.Is(e, service.ErrPluginInstanceNotFound) {
		return service.ErrPluginTaskConflict
	}
	if e != nil {
		return e
	}
	stored, e := scanPluginTaskRun(tx.QueryRowContext(ctx, `SELECT `+pluginTaskRunColumns+` FROM sub2api_plugin_task_runs WHERE id=$1 FOR UPDATE`, run.ID))
	if errors.Is(e, service.ErrPluginTaskNotFound) {
		return service.ErrPluginTaskConflict
	}
	if e != nil {
		return e
	}
	var instanceGen int64
	if e = tx.QueryRowContext(ctx, `SELECT instance_generation FROM sub2api_plugin_task_runs WHERE id=$1`, run.ID).Scan(&instanceGen); e != nil {
		return e
	}
	now, e := pluginTaskDBNow(ctx, tx)
	if e != nil {
		return e
	}
	reject := func() error {
		_ = tx.Rollback()
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if e := r.reapClaim(cleanup, c); e != nil {
			return errors.Join(service.ErrPluginTaskConflict, e)
		}
		return service.ErrPluginTaskConflict
	}
	if instanceGen != c.InstanceGeneration || !pluginTaskClaimValid(a, instance, c, stored, now) {
		return reject()
	}
	if run.Status == "succeeded" && callback != nil {
		if e = callback(tx); e != nil {
			return e
		}
	}
	// Re-sample after callback: a transaction started before expiry is not a lease.
	// Also reject authority mutations made by the callback itself. Row locks
	// exclude other sessions, but not SQL executed on this same transaction.
	a, instance, e = lockPluginTask(ctx, tx, c.Task.ID)
	if e != nil {
		if errors.Is(e, service.ErrPluginTaskNotFound) || errors.Is(e, service.ErrPluginInstanceNotFound) {
			return reject()
		}
		return e
	}
	now, e = pluginTaskDBNow(ctx, tx)
	if e != nil {
		return e
	}
	if !pluginTaskClaimValid(a, instance, c, stored, now) {
		return reject()
	}
	var retry any
	if run.Status == "failed" && stored.Attempt <= a.task.MaxRetries {
		retry = now.Add(pluginTaskRetryDelay(a.task, stored.Attempt))
	}
	changed := run.Status == "succeeded" && run.Changed
	_, e = tx.ExecContext(ctx, `UPDATE sub2api_plugin_task_runs SET status=$2,changed=$3,error_code=$4,result_encrypted=$5,finished_at=$6,duration_ms=GREATEST(0,(EXTRACT(EPOCH FROM ($6::timestamptz-started_at))*1000)::bigint),retry_at=$7 WHERE id=$1`, run.ID, run.Status, changed, run.ErrorCode, run.ResultEncrypted, now, retry)
	if e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, `UPDATE sub2api_plugin_tasks SET lease_owner=NULL,lease_until=NULL WHERE id=$1 AND fencing_token=$2`, a.task.ID, a.fence)
	if e != nil {
		return e
	}
	return tx.Commit()
}
func (r *pluginTaskRepository) Runs(ctx context.Context, taskID int64, limit int) ([]service.PluginTaskRun, error) {
	if taskID <= 0 {
		return nil, service.ErrPluginTaskInvalid
	}
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	rows, e := r.db.QueryContext(ctx, `SELECT `+pluginTaskRunColumns+` FROM sub2api_plugin_task_runs WHERE task_id=$1 ORDER BY id DESC LIMIT $2`, taskID, limit)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	runs := make([]service.PluginTaskRun, 0)
	for rows.Next() {
		run, e := scanPluginTaskRun(rows)
		if e != nil {
			return nil, e
		}
		runs = append(runs, *run)
	}
	return runs, rows.Err()
}
func (r *pluginTaskRepository) Run(ctx context.Context, id int64) (*service.PluginTaskRun, error) {
	if id <= 0 {
		return nil, service.ErrPluginTaskInvalid
	}
	return scanPluginTaskRun(r.db.QueryRowContext(ctx, `SELECT `+pluginTaskRunColumns+` FROM sub2api_plugin_task_runs WHERE id=$1`, id))
}
func (r *pluginTaskRepository) Due(ctx context.Context, limit int) ([]int64, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 100 {
		limit = 100
	}
	// Bounded scavenging includes disabled tasks and unpublished instances.
	rows, e := r.db.QueryContext(ctx, `SELECT t.id FROM sub2api_plugin_tasks t JOIN sub2api_plugin_instances i ON i.id=t.instance_id JOIN sub2api_plugin_task_runs r ON r.task_id=t.id AND r.status='running' WHERE t.lease_until IS NULL OR t.lease_until<=clock_timestamp() OR NOT t.enabled OR NOT i.enabled OR t.generation<>r.task_generation OR i.generation<>r.instance_generation OR i.current_revision<>r.config_revision ORDER BY t.id LIMIT $1`, limit)
	if e != nil {
		return nil, e
	}
	stale := []int64{}
	for rows.Next() {
		var id int64
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return nil, e
		}
		stale = append(stale, id)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	for _, id := range stale {
		tx, e := r.db.BeginTx(ctx, nil)
		if e != nil {
			return nil, e
		}
		a, i, e := lockPluginTask(ctx, tx, id)
		if errors.Is(e, service.ErrPluginTaskNotFound) || errors.Is(e, service.ErrPluginInstanceNotFound) {
			tx.Rollback()
			continue
		}
		if e != nil {
			tx.Rollback()
			return nil, e
		}
		now, e := pluginTaskDBNow(ctx, tx)
		if e == nil {
			e = pluginTaskReap(ctx, tx, a, i, now)
		}
		if e != nil {
			tx.Rollback()
			return nil, e
		}
		if e = tx.Commit(); e != nil {
			return nil, e
		}
	}
	rows, e = r.db.QueryContext(ctx, `SELECT t.id FROM sub2api_plugin_tasks t JOIN sub2api_plugin_instances i ON i.id=t.instance_id WHERE t.enabled AND i.enabled AND (t.lease_until IS NULL OR t.lease_until<=clock_timestamp()) AND NOT EXISTS(SELECT 1 FROM sub2api_plugin_task_runs active WHERE active.task_id=t.id AND active.status='running') AND ((t.next_run_at IS NOT NULL AND t.next_run_at<=clock_timestamp()) OR EXISTS(SELECT 1 FROM sub2api_plugin_task_runs r WHERE r.task_id=t.id AND r.status IN ('failed','lost') AND r.attempt<=t.max_retries AND r.retry_at<=clock_timestamp() AND r.task_generation=t.generation AND r.instance_generation=i.generation AND r.config_revision=i.current_revision AND NOT EXISTS(SELECT 1 FROM sub2api_plugin_task_runs newer WHERE newer.task_id=r.task_id AND newer.idempotency_key=r.idempotency_key AND newer.attempt>r.attempt))) ORDER BY t.next_run_at NULLS FIRST,t.id LIMIT $1`, limit)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	ids := make([]int64, 0)
	for rows.Next() {
		var id int64
		if e = rows.Scan(&id); e != nil {
			return nil, e
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// RetryKeys discovers logical retries in SQL, independently of the bounded
// public run-history DTO. The NOT EXISTS condition and unique attempt key give
// exactly one latest attempt per logical key. Claim rechecks this authority.
func (r *pluginTaskRepository) RetryKeys(ctx context.Context, taskID int64, limit int) ([]string, error) {
	if taskID <= 0 {
		return nil, service.ErrPluginTaskInvalid
	}
	if limit <= 0 {
		limit = service.PluginTaskPollLimit
	}
	if limit > 100 {
		limit = 100
	}
	rows, e := r.db.QueryContext(ctx, `SELECT r.idempotency_key FROM sub2api_plugin_tasks t JOIN sub2api_plugin_instances i ON i.id=t.instance_id JOIN sub2api_plugin_task_runs r ON r.task_id=t.id WHERE t.id=$1 AND t.enabled AND i.enabled AND (t.lease_until IS NULL OR t.lease_until<=clock_timestamp()) AND NOT EXISTS(SELECT 1 FROM sub2api_plugin_task_runs active WHERE active.task_id=t.id AND active.status='running') AND r.status IN ('failed','lost') AND r.attempt<=t.max_retries AND r.retry_at<=clock_timestamp() AND r.task_generation=t.generation AND r.instance_generation=i.generation AND r.config_revision=i.current_revision AND NOT EXISTS(SELECT 1 FROM sub2api_plugin_task_runs newer WHERE newer.task_id=r.task_id AND newer.idempotency_key=r.idempotency_key AND newer.attempt>r.attempt) ORDER BY r.retry_at,r.id LIMIT $2`, taskID, limit)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	keys := make([]string, 0)
	for rows.Next() {
		var key string
		if e = rows.Scan(&key); e != nil {
			return nil, e
		}
		keys = append(keys, key)
	}
	return keys, rows.Err()
}

var _ service.PluginTaskRetryKeyRepository = (*pluginTaskRepository)(nil)
