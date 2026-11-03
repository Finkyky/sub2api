package repository

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type piRepositoryDeleteAPI interface {
	Delete(context.Context, int64, int64) error
}

func TestPluginInstanceRepositoryDeleteGuards(t *testing.T) {
	for _, tc := range []struct {
		name    string
		enabled bool
		gen     int64
		missing bool
		want    string
	}{
		{"enabled", true, 1, false, "plugin instance is enabled"},
		{"stale", false, 2, false, service.ErrPluginInstanceConflict.Error()},
		{"missing", false, 1, true, service.ErrPluginInstanceNotFound.Error()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, m := piMock(t)
			api, ok := r.(piRepositoryDeleteAPI)
			require.True(t, ok, "repository must implement Delete")
			m.ExpectBegin()
			q := m.ExpectQuery(`SELECT.*FROM sub2api_plugin_instances.*FOR UPDATE`).WithArgs(int64(11))
			if tc.missing {
				q.WillReturnError(sql.ErrNoRows)
			} else {
				q.WillReturnRows(sqlmock.NewRows([]string{"id", "plugin_id", "name", "generation", "current_revision", "latest_revision", "enabled"}).AddRow(11, 3, "fixture", tc.gen, 1, 1, tc.enabled))
			}
			m.ExpectRollback()
			require.EqualError(t, api.Delete(context.Background(), 11, 1), tc.want)
			require.NoError(t, m.ExpectationsWereMet())
		})
	}
}
func TestPluginInstanceRepositoryDeleteCommitAndRollback(t *testing.T) {
	for _, failure := range []string{"", "begin", "read", "exec", "commit"} {
		t.Run("failure_"+failure, func(t *testing.T) {
			r, m := piMock(t)
			api, ok := r.(piRepositoryDeleteAPI)
			require.True(t, ok)
			fail := errors.New("transaction failure")
			begin := m.ExpectBegin()
			if failure == "begin" {
				begin.WillReturnError(fail)
			} else {
				q := m.ExpectQuery(`SELECT.*FROM sub2api_plugin_instances.*FOR UPDATE`).WithArgs(int64(11))
				if failure == "read" {
					q.WillReturnError(fail)
					m.ExpectRollback()
				} else {
					q.WillReturnRows(sqlmock.NewRows([]string{"id", "plugin_id", "name", "generation", "current_revision", "latest_revision", "enabled"}).AddRow(11, 3, "fixture", 1, 1, 2, false))
					ex := m.ExpectExec(`DELETE FROM sub2api_plugin_instances WHERE id=\$1`).WithArgs(int64(11))
					if failure == "exec" {
						ex.WillReturnError(fail)
						m.ExpectRollback()
					} else {
						ex.WillReturnResult(sqlmock.NewResult(0, 1))
						commit := m.ExpectCommit()
						if failure == "commit" {
							commit.WillReturnError(fail)
						}
					}
				}
			}
			err := api.Delete(context.Background(), 11, 1)
			if failure == "" {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, fail)
			}
			require.NoError(t, m.ExpectationsWereMet())
		})
	}
}
func TestPluginInstanceRepositoryDeleteInvalid(t *testing.T) {
	r, m := piMock(t)
	api, ok := r.(piRepositoryDeleteAPI)
	require.True(t, ok)
	for _, a := range [][2]int64{{0, 1}, {11, 0}, {-1, 1}, {11, -1}} {
		require.ErrorIs(t, api.Delete(context.Background(), a[0], a[1]), service.ErrPluginInstanceInvalid)
	}
	require.NoError(t, m.ExpectationsWereMet())
}

func TestPluginInstanceRepositoryRevisionSummariesMissingAfterDelete(t *testing.T) {
	r, m := piMock(t)
	m.ExpectQuery(`SELECT instance_id, revision, config_version FROM sub2api_plugin_instance_revisions`).WithArgs(int64(11)).WillReturnRows(sqlmock.NewRows([]string{"instance_id", "revision", "config_version"}))
	_, err := r.RevisionSummaries(context.Background(), 11)
	require.ErrorIs(t, err, service.ErrPluginInstanceNotFound)
	require.NoError(t, m.ExpectationsWereMet())
}
