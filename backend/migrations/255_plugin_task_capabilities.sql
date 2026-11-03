-- M5 host capability storage; manifest declarations do not grant privileges.
-- Run/task IDs intentionally have no FK dependency on migration 243: immutable
-- audit survives scheduler run retention, task deletion, and pricing deletion.
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '10min';

CREATE TABLE IF NOT EXISTS sub2api_plugin_task_kv (
    instance_id BIGINT NOT NULL REFERENCES sub2api_plugin_instances(id) ON DELETE CASCADE,
    key VARCHAR(160) NOT NULL CHECK (length(btrim(key)) > 0),
    value JSONB NOT NULL CHECK (octet_length(value::text) <= 32768),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (instance_id, key)
);
CREATE TABLE IF NOT EXISTS sub2api_plugin_task_capability_commits (
    instance_id BIGINT NOT NULL REFERENCES sub2api_plugin_instances(id) ON DELETE CASCADE,
    idempotency_key VARCHAR(256) NOT NULL,
    run_id BIGINT NOT NULL,
    task_id BIGINT NOT NULL,
    output_hash CHAR(64) NOT NULL,
    source_plugin TEXT NOT NULL,
    source_entry VARCHAR(64) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (instance_id, task_id, idempotency_key)
);
CREATE TABLE IF NOT EXISTS sub2api_plugin_pricing_versions (
    id BIGSERIAL PRIMARY KEY,
    run_id BIGINT NOT NULL,
    instance_id BIGINT NOT NULL,
    task_id BIGINT NOT NULL,
    idempotency_key VARCHAR(256) NOT NULL,
    channel_id BIGINT NOT NULL,
    pricing_id BIGINT NOT NULL,
    old_values JSONB NOT NULL CHECK (jsonb_typeof(old_values) = 'object'),
    new_values JSONB NOT NULL CHECK (jsonb_typeof(new_values) = 'object'),
    source_plugin TEXT NOT NULL,
    source_version TEXT NOT NULL,
    source_entry VARCHAR(64) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (instance_id, task_id, idempotency_key, pricing_id)
);
CREATE INDEX IF NOT EXISTS idx_plugin_pricing_versions_history
    ON sub2api_plugin_pricing_versions (channel_id, pricing_id, id DESC);
CREATE INDEX IF NOT EXISTS idx_plugin_pricing_versions_run
    ON sub2api_plugin_pricing_versions (run_id, instance_id);
COMMENT ON TABLE sub2api_plugin_pricing_versions IS 'Authorized task price changes only; never arbitrary raw sensitive output';
