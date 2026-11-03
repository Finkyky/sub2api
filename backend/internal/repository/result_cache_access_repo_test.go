package repository

import (
	"context"
	"errors"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
	"time"
)

func TestResultCacheAccessNoDoubleBilling(t *testing.T) {
	for _, upstream := range []bool{false, true} {
		t.Run(map[bool]string{false: "HIT", true: "MISS"}[upstream], func(t *testing.T) {
			var query string
			db, m, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherFunc(func(expected, actual string) error {
				query = actual
				return sqlmock.QueryMatcherRegexp.Match(expected, actual)
			})))
			require.NoError(t, err)
			defer db.Close()
			a := service.ResultCacheAccess{RequestID: "current", UserID: 1, APIKeyID: 2, GroupID: 3, Model: "m", Mode: "read_write", Scope: "user", Status: map[bool]string{false: "HIT", true: "MISS"}[upstream], SourceRequestID: "source", AgeSeconds: 5, DurationMS: 10, UpstreamCalled: upstream}
			var cost any
			if !upstream {
				cost = 0.0
			}
			m.ExpectExec(`(?s)INSERT INTO result_cache_access_logs.*VALUES.*\$14,0\).*ON CONFLICT\(request_id\) DO NOTHING`).WithArgs(a.RequestID, a.UserID, a.APIKeyID, a.GroupID, a.Model, a.Mode, a.Scope, a.Status, a.Reason, a.SourceRequestID, a.AgeSeconds, a.DurationMS, a.UpstreamCalled, cost).WillReturnResult(sqlmock.NewResult(1, 1))
			require.NoError(t, NewResultCacheAccessRepository(db).Append(context.Background(), a))
			for _, field := range []string{"account_id", "usage_logs", "cache_read_tokens", "authorization", "prompt", "body"} {
				require.NotContains(t, strings.ToLower(query), field)
			}
			require.NoError(t, m.ExpectationsWereMet())
		})
	}
}
func TestResultCacheAccessDuplicateIsNoOp(t *testing.T) {
	db, m, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	m.ExpectExec(`ON CONFLICT\(request_id\) DO NOTHING`).WillReturnResult(sqlmock.NewResult(0, 0))
	require.NoError(t, NewResultCacheAccessRepository(db).Append(context.Background(), service.ResultCacheAccess{RequestID: "already-recorded"}))
	require.NoError(t, m.ExpectationsWereMet())
}
func TestResultCacheAccessRetentionQuery(t *testing.T) {
	db, m, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	before := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	m.ExpectExec(`DELETE FROM result_cache_access_logs.*created_at<\$1 ORDER BY created_at LIMIT 10000`).WithArgs(before).WillReturnResult(sqlmock.NewResult(0, 7))
	require.NoError(t, NewResultCacheAccessRepository(db).Cleanup(context.Background(), before))
	require.NoError(t, m.ExpectationsWereMet())
}
func TestResultCacheAccessWriteError(t *testing.T) {
	db, m, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	failure := errors.New("audit database unavailable")
	m.ExpectExec(`INSERT INTO result_cache_access_logs`).WillReturnError(failure)
	require.ErrorIs(t, NewResultCacheAccessRepository(db).Append(context.Background(), service.ResultCacheAccess{}), failure)
	require.NoError(t, m.ExpectationsWereMet())
}
