//go:build integration

package repository

import (
	"context"
	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestGroupResultCacheMigrationsIdempotentIntegration(t *testing.T) {
	ctx := context.Background()
	tx, err := integrationDB.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer tx.Rollback()
	for round := 0; round < 2; round++ {
		for _, name := range []string{"251_group_response_result_cache.sql", "252_result_cache_access_logs.sql"} {
			sql, err := migrations.FS.ReadFile(name)
			require.NoError(t, err)
			_, err = tx.ExecContext(ctx, string(sql))
			require.NoError(t, err, "migration %s round %d", name, round)
		}
	}
	var constraints int
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM pg_constraint WHERE conname='groups_result_cache_config_object' AND conrelid='groups'::regclass`).Scan(&constraints))
	require.Equal(t, 1, constraints)
	var config string
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT column_default FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='groups' AND column_name='response_result_cache_config'`).Scan(&config))
	require.Contains(t, config, `"enabled": false`)
	require.Contains(t, config, `"revision": 1`)
}
