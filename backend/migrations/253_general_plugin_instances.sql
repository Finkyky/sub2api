-- General instances are separate from exclusive transport bindings.
-- Config revisions are append-only in the repository and encrypted at rest.
CREATE TABLE IF NOT EXISTS sub2api_plugin_instances (
    id BIGSERIAL PRIMARY KEY,
    plugin_id BIGINT NOT NULL REFERENCES sub2api_plugin_installations(id) ON DELETE RESTRICT,
    name VARCHAR(160) NOT NULL CHECK (length(btrim(name)) > 0),
    generation BIGINT NOT NULL DEFAULT 1 CHECK (generation > 0),
    current_revision BIGINT NOT NULL DEFAULT 1 CHECK (current_revision > 0),
    latest_revision BIGINT NOT NULL DEFAULT 1 CHECK (latest_revision >= current_revision),
    enabled BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_sub2api_plugin_instances_plugin ON sub2api_plugin_instances(plugin_id);

CREATE TABLE IF NOT EXISTS sub2api_plugin_instance_revisions (
    instance_id BIGINT NOT NULL REFERENCES sub2api_plugin_instances(id) ON DELETE CASCADE,
    revision BIGINT NOT NULL CHECK (revision > 0),
    config_version INTEGER NOT NULL CHECK (config_version > 0),
    config_encrypted TEXT NOT NULL CHECK (length(config_encrypted) BETWEEN 1 AND 262144),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (instance_id, revision)
);
-- Deferred because an instance and its first revision are created together.
DO $$ BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'sub2api_plugin_instances_current_revision_fk'
                   AND conrelid = 'sub2api_plugin_instances'::regclass) THEN
        ALTER TABLE sub2api_plugin_instances ADD CONSTRAINT sub2api_plugin_instances_current_revision_fk
            FOREIGN KEY (id, current_revision) REFERENCES sub2api_plugin_instance_revisions(instance_id, revision)
            DEFERRABLE INITIALLY DEFERRED;
    END IF;
END $$;

CREATE TABLE IF NOT EXISTS sub2api_plugin_group_bindings (
    id BIGSERIAL PRIMARY KEY,
    instance_id BIGINT NOT NULL REFERENCES sub2api_plugin_instances(id) ON DELETE CASCADE,
    group_id BIGINT NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    stage VARCHAR(16) NOT NULL CHECK (stage IN ('input', 'output')),
    entry VARCHAR(64) NOT NULL CHECK (length(entry) > 0),
    protocol VARCHAR(160) NOT NULL CHECK (length(protocol) > 0),
    sort_order INTEGER NOT NULL DEFAULT 0 CHECK (sort_order BETWEEN -100000 AND 100000),
    enabled BOOLEAN NOT NULL DEFAULT FALSE,
    can_replace BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (instance_id, group_id, stage, entry, protocol)
);
CREATE INDEX IF NOT EXISTS idx_sub2api_plugin_group_bindings_instance ON sub2api_plugin_group_bindings(instance_id);
CREATE INDEX IF NOT EXISTS idx_sub2api_plugin_group_bindings_group_stage
    ON sub2api_plugin_group_bindings(group_id, stage, sort_order, id) WHERE enabled;
