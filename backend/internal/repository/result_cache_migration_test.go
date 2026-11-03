package repository

import (
	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestGroupResultCacheMigrationConstraintGuard(t *testing.T) {
	sql, err := migrations.FS.ReadFile("251_group_response_result_cache.sql")
	require.NoError(t, err)
	require.Regexp(t, `(?s)DO.*IF NOT EXISTS.*pg_constraint.*conname = 'groups_result_cache_config_object'.*conrelid = 'groups'::regclass.*ALTER TABLE groups ADD CONSTRAINT groups_result_cache_config_object`, string(sql), "repeat execution must guard this exact table's constraint")
}
