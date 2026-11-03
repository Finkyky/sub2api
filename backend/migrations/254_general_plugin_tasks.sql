-- Task authority and business writes are fenced in one PostgreSQL transaction.
CREATE TABLE IF NOT EXISTS sub2api_plugin_tasks (
 id BIGSERIAL PRIMARY KEY,
 instance_id BIGINT NOT NULL REFERENCES sub2api_plugin_instances(id) ON DELETE CASCADE,
 name VARCHAR(160) NOT NULL CHECK (length(btrim(name)) > 0),
 entry VARCHAR(64) NOT NULL CHECK (length(entry) > 0),
 cron VARCHAR(256) NOT NULL DEFAULT '',
 enabled BOOLEAN NOT NULL DEFAULT FALSE,
 timeout_seconds INTEGER NOT NULL CHECK (timeout_seconds BETWEEN 1 AND 300),
 max_retries INTEGER NOT NULL DEFAULT 0 CHECK (max_retries BETWEEN 0 AND 10),
 retry_backoff_seconds INTEGER NOT NULL DEFAULT 1 CHECK (retry_backoff_seconds BETWEEN 1 AND 3600),
 generation BIGINT NOT NULL DEFAULT 1 CHECK (generation > 0),
 input JSONB NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(input) = 'object' AND octet_length(input::text) <= 262144),
 scope JSONB NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(scope) = 'object' AND octet_length(scope::text) <= 262144),
 next_run_at TIMESTAMPTZ,
 fencing_token BIGINT NOT NULL DEFAULT 0 CHECK (fencing_token >= 0),
 lease_owner VARCHAR(160),
 lease_until TIMESTAMPTZ,
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 CHECK ((lease_owner IS NULL) = (lease_until IS NULL))
);
CREATE INDEX IF NOT EXISTS idx_sub2api_plugin_tasks_instance ON sub2api_plugin_tasks(instance_id,id);
CREATE INDEX IF NOT EXISTS idx_sub2api_plugin_tasks_due ON sub2api_plugin_tasks(next_run_at,id) WHERE enabled;
CREATE INDEX IF NOT EXISTS idx_sub2api_plugin_tasks_lease ON sub2api_plugin_tasks(lease_until,id) WHERE lease_until IS NOT NULL;
CREATE TABLE IF NOT EXISTS sub2api_plugin_task_runs (
 id BIGSERIAL PRIMARY KEY,
 task_id BIGINT NOT NULL REFERENCES sub2api_plugin_tasks(id) ON DELETE CASCADE,
 instance_id BIGINT NOT NULL,
 instance_generation BIGINT NOT NULL,
 config_revision BIGINT NOT NULL,
 task_generation BIGINT NOT NULL,
 trigger VARCHAR(16) NOT NULL CHECK (trigger IN ('manual','scheduled','retry')),
 idempotency_key VARCHAR(256) NOT NULL CHECK (length(btrim(idempotency_key)) > 0),
 attempt INTEGER NOT NULL CHECK (attempt BETWEEN 1 AND 11),
 fencing_token BIGINT NOT NULL CHECK (fencing_token > 0),
 status VARCHAR(16) NOT NULL CHECK (status IN ('running','succeeded','failed','lost')),
 changed BOOLEAN NOT NULL DEFAULT FALSE,
 error_code VARCHAR(64) NOT NULL DEFAULT '',
 started_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 finished_at TIMESTAMPTZ,
 duration_ms BIGINT NOT NULL DEFAULT 0 CHECK (duration_ms >= 0),
 result_encrypted TEXT NOT NULL DEFAULT '' CHECK (octet_length(result_encrypted) <= 2097152),
 retry_at TIMESTAMPTZ,
 UNIQUE(task_id,idempotency_key,attempt),
 UNIQUE(task_id,fencing_token)
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_sub2api_plugin_task_one_running ON sub2api_plugin_task_runs(task_id) WHERE status='running';
CREATE UNIQUE INDEX IF NOT EXISTS idx_sub2api_plugin_task_one_success ON sub2api_plugin_task_runs(task_id,idempotency_key) WHERE status='succeeded';
CREATE INDEX IF NOT EXISTS idx_sub2api_plugin_task_runs_history ON sub2api_plugin_task_runs(task_id,id DESC);
CREATE INDEX IF NOT EXISTS idx_sub2api_plugin_task_runs_retry ON sub2api_plugin_task_runs(retry_at,task_id) WHERE status IN ('failed','lost');
-- Intentionally survives task deletion; contains field names, never their values.
CREATE TABLE IF NOT EXISTS sub2api_plugin_task_audit (
 id BIGSERIAL PRIMARY KEY,
 task_id BIGINT NOT NULL,
 instance_id BIGINT NOT NULL,
 generation BIGINT NOT NULL,
 action VARCHAR(16) NOT NULL CHECK (action IN ('create','update','delete')),
 changed_fields JSONB NOT NULL CHECK (jsonb_typeof(changed_fields)='array'),
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_sub2api_plugin_task_audit_task ON sub2api_plugin_task_audit(task_id,id);
