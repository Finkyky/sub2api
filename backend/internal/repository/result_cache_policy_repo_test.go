package repository

import (
	"context"
	"database/sql"
	"errors"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
	"regexp"
	"strings"
	"testing"
)

func TestGroupResultCachePolicyCurrent(t *testing.T) {
	db, m, e := sqlmock.New()
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	repo := NewResultCachePolicyRepository(db)
	m.ExpectQuery("SELECT.*response_result_cache_config").WithArgs(int64(7)).WillReturnRows(sqlmock.NewRows([]string{"config", "status", "version"}).AddRow(`{"enabled":true,"ttl_seconds":30,"max_response_bytes":2097152,"revision":4}`, "active", "route-v4"))
	p, e := repo.Current(context.Background(), 7)
	if e != nil || !p.Active || !p.Config.Enabled || p.Config.Revision != 4 {
		t.Fatal(p, e)
	}
	if e = m.ExpectationsWereMet(); e != nil {
		t.Fatal(e)
	}
}

func TestResultCachePolicyVersionSourceContract(t *testing.T) {
	for _, part := range []string{"effective_route_revision", "input_policy_revision", "result-cache-policy-v3", "sha256(convert_to", "to_jsonb(a)", "a.extra", "ag.priority", "to_jsonb(g)", "to_jsonb(c)", "to_jsonb(cp)", "s.key, s.value", "ORDER BY a.id", "ORDER BY c.id", "ORDER BY cp.id", "ORDER BY s.key"} {
		require.Contains(t, resultCachePolicyCurrentSQL, part)
	}
	require.NotContains(t, resultCachePolicyCurrentSQL, "MAX(updated_at)")
	require.NotContains(t, resultCachePolicyCurrentSQL, "a.updated_at")
	require.Contains(t, resultCachePolicyCurrentSQL, "ARRAY['updated_at', 'last_used_at', 'extra']::text[]")
	for _, key := range []string{"quota_used", "quota_daily_used", "quota_weekly_used", "quota_daily_start", "quota_weekly_start", "quota_daily_reset_at", "quota_weekly_reset_at"} {
		require.Contains(t, resultCachePolicyCurrentSQL, "'"+key+"'")
	}
	db, m, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	version := strings.Repeat("a", 64)
	m.ExpectQuery(regexp.QuoteMeta(resultCachePolicyCurrentSQL)).WithArgs(int64(7)).WillReturnRows(sqlmock.NewRows([]string{"config", "status", "version"}).AddRow(`{"enabled":true,"ttl_seconds":30,"max_response_bytes":8192,"revision":4}`, "active", version))
	p, err := NewResultCachePolicyRepository(db).Current(context.Background(), 7)
	require.NoError(t, err)
	require.Equal(t, version, p.Version)
	require.True(t, p.Active)
	require.NoError(t, m.ExpectationsWereMet())
}
func TestResultCachePolicyCurrentErrors(t *testing.T) {
	for _, name := range []string{"missing", "query_error", "invalid_json", "invalid_config", "inactive"} {
		t.Run(name, func(t *testing.T) {
			db, m, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()
			expected := m.ExpectQuery(regexp.QuoteMeta(resultCachePolicyCurrentSQL)).WithArgs(int64(7))
			switch name {
			case "missing":
				expected.WillReturnError(sql.ErrNoRows)
			case "query_error":
				expected.WillReturnError(errors.New("fixture database read failure"))
			case "invalid_json":
				expected.WillReturnRows(sqlmock.NewRows([]string{"config", "status", "version"}).AddRow(`{`, "active", "hash"))
			case "invalid_config":
				expected.WillReturnRows(sqlmock.NewRows([]string{"config", "status", "version"}).AddRow(`{"enabled":true,"ttl_seconds":999999}`, "active", "hash"))
			case "inactive":
				expected.WillReturnRows(sqlmock.NewRows([]string{"config", "status", "version"}).AddRow(`{}`, "disabled", "hash"))
			}
			p, err := NewResultCachePolicyRepository(db).Current(context.Background(), 7)
			if name == "inactive" {
				require.NoError(t, err)
				require.False(t, p.Active)
			} else {
				require.Error(t, err)
			}
			if name == "missing" {
				require.ErrorIs(t, err, sql.ErrNoRows)
			}
			require.NoError(t, m.ExpectationsWereMet())
		})
	}
}
