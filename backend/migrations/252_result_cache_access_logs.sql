CREATE TABLE IF NOT EXISTS result_cache_access_logs (
 id BIGSERIAL PRIMARY KEY,
 request_id VARCHAR(64) NOT NULL UNIQUE,
 user_id BIGINT NOT NULL,
 api_key_id BIGINT NOT NULL,
 group_id BIGINT NOT NULL,
 model VARCHAR(200) NOT NULL,
 mode VARCHAR(16) NOT NULL,
 scope VARCHAR(16) NOT NULL,
 status VARCHAR(16) NOT NULL,
 reason VARCHAR(64) NOT NULL DEFAULT '',
 source_request_id VARCHAR(64) NOT NULL DEFAULT '',
 age_seconds BIGINT NOT NULL DEFAULT 0,
 duration_ms BIGINT NOT NULL DEFAULT 0,
 upstream_called BOOLEAN NOT NULL DEFAULT FALSE,
 upstream_cost DECIMAL(20,10),
 cache_charge DECIMAL(20,10) NOT NULL DEFAULT 0,
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS result_cache_access_logs_created_at_idx ON result_cache_access_logs(created_at);
CREATE INDEX IF NOT EXISTS result_cache_access_logs_group_created_idx ON result_cache_access_logs(group_id,created_at);
COMMENT ON TABLE result_cache_access_logs IS 'Result-cache metadata only. Never stores prompt/response bodies; not a second generation billing ledger.';
