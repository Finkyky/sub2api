package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	contract "github.com/Wei-Shaw/sub2api/pkg/pluginapi/contracts/v1"
	"github.com/stretchr/testify/require"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func piMock(t *testing.T) (service.PluginInstanceRepository, sqlmock.Sqlmock) {
	t.Helper()
	db, m, e := sqlmock.New()
	require.NoError(t, e)
	t.Cleanup(func() { db.Close() })
	return NewPluginInstanceRepository(db), m
}
func piStateRows(generation int64) *sqlmock.Rows {
	return sqlmock.NewRows([]string{"id", "plugin_id", "name", "generation", "current_revision", "latest_revision", "enabled"}).AddRow(11, 3, "check", generation, 1, 1, true)
}
func TestPluginInstanceRepositoryConflict(t *testing.T) {
	for _, operation := range []string{"save", "publish", "bindings"} {
		t.Run(operation, func(t *testing.T) {
			r, m := piMock(t)
			m.ExpectBegin()
			m.ExpectQuery(`SELECT.*FROM sub2api_plugin_instances.*FOR UPDATE`).WithArgs(int64(11)).WillReturnRows(piStateRows(4))
			m.ExpectRollback()
			var e error
			switch operation {
			case "save":
				_, e = r.SaveRevision(context.Background(), 11, 3, "encrypted")
			case "publish":
				_, e = r.Publish(context.Background(), 11, 3, 1, true)
			case "bindings":
				_, e = r.ReplaceBindings(context.Background(), 11, 3, nil)
			}
			require.ErrorIs(t, e, service.ErrPluginInstanceConflict)
			require.NoError(t, m.ExpectationsWereMet())
		})
	}
}
func TestPluginInstanceRepositoryFailedRevisionRollsBack(t *testing.T) {
	r, m := piMock(t)
	m.ExpectBegin()
	m.ExpectQuery(`SELECT.*FROM sub2api_plugin_instances.*FOR UPDATE`).WillReturnRows(piStateRows(4))
	m.ExpectExec(`INSERT INTO sub2api_plugin_instance_revisions`).WillReturnError(errors.New("write failed"))
	m.ExpectRollback()
	_, e := r.SaveRevision(context.Background(), 11, 4, "encrypted")
	require.ErrorContains(t, e, "write failed")
	require.NoError(t, m.ExpectationsWereMet())
}
func TestPluginInstanceRepositoryMissing(t *testing.T) {
	r, m := piMock(t)
	m.ExpectBegin()
	m.ExpectQuery(`SELECT.*FROM sub2api_plugin_instances`).WillReturnError(sql.ErrNoRows)
	m.ExpectRollback()
	_, e := r.Snapshot(context.Background(), 11, 0)
	require.ErrorIs(t, e, service.ErrPluginInstanceNotFound)
	require.NoError(t, m.ExpectationsWereMet())
}

func TestPluginInstanceRepositoryList(t *testing.T) {
	r, m := piMock(t)
	m.ExpectQuery(`SELECT id, plugin_id, name, generation, current_revision, latest_revision, enabled FROM sub2api_plugin_instances WHERE plugin_id = \$1 ORDER BY id`).
		WithArgs(int64(3)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "plugin_id", "name", "generation", "current_revision", "latest_revision", "enabled"}).
			AddRow(11, 3, "check", 1, 1, 1, false).
			AddRow(12, 3, "rewrite", 2, 2, 3, true))
	instances, e := r.List(context.Background(), 3)
	require.NoError(t, e)
	require.Equal(t, []service.PluginInstance{
		{ID: 11, PluginID: 3, Name: "check", Generation: 1, CurrentRevision: 1, LatestRevision: 1, Enabled: false},
		{ID: 12, PluginID: 3, Name: "rewrite", Generation: 2, CurrentRevision: 2, LatestRevision: 3, Enabled: true},
	}, instances)
	require.NoError(t, m.ExpectationsWereMet())
}

func TestPluginInstanceRepositoryRevisionSummaries(t *testing.T) {
	r, m := piMock(t)
	m.ExpectQuery(`SELECT instance_id, revision, config_version FROM sub2api_plugin_instance_revisions WHERE instance_id = \$1 ORDER BY revision`).
		WithArgs(int64(11)).
		WillReturnRows(sqlmock.NewRows([]string{"instance_id", "revision", "config_version"}).
			AddRow(11, 1, 2).
			AddRow(11, 2, 3))
	revisions, e := r.RevisionSummaries(context.Background(), 11)
	require.NoError(t, e)
	require.Equal(t, []service.PluginInstanceRevisionSummary{
		{InstanceID: 11, Revision: 1, ConfigVersion: 2},
		{InstanceID: 11, Revision: 2, ConfigVersion: 3},
	}, revisions)
	require.NoError(t, m.ExpectationsWereMet())
}

func TestPluginInstanceRepositoryRejectsInvalidReadIDs(t *testing.T) {
	r, _ := piMock(t)
	_, e := r.RevisionSummaries(context.Background(), 0)
	require.ErrorIs(t, e, service.ErrPluginInstanceInvalid)
}

func TestPluginInstanceMigrationStructure(t *testing.T) {
	p := filepath.Join("..", "..", "migrations", "253_general_plugin_instances.sql")
	b, e := os.ReadFile(p)
	require.NoError(t, e)
	s := string(b)
	for _, v := range []string{"sub2api_plugin_instances", "sub2api_plugin_instance_revisions", "sub2api_plugin_group_bindings", "ON DELETE RESTRICT", "DEFERRABLE INITIALLY DEFERRED", "CHECK (stage IN ('input', 'output'))", "REFERENCES groups(id)"} {
		require.Contains(t, s, v)
	}
	require.NotContains(t, s, "DROP TABLE")
	require.NotContains(t, s, "ALTER TABLE sub2api_plugin_bindings")
}

func TestPluginInstanceInstallationSerializesWithCreation(t *testing.T) {
	db, m, e := sqlmock.New()
	require.NoError(t, e)
	defer db.Close()
	m.ExpectBegin()
	m.ExpectExec(`SELECT pg_advisory_xact_lock`).WithArgs("sub2api:plugin-install:com.example.general").WillReturnResult(sqlmock.NewResult(0, 1))
	m.ExpectQuery(`SELECT id FROM sub2api_plugin_installations.*FOR UPDATE`).WithArgs("com.example.general").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(3))
	m.ExpectQuery(`(?s)INSERT INTO sub2api_plugin_installations.*sub2api_plugin_instances`).WillReturnError(errors.New("frozen package"))
	m.ExpectRollback()
	_, e = NewPluginRepository(db).Install(context.Background(), &service.PluginInstallation{PluginKey: "com.example.general"}, nil)
	require.EqualError(t, e, "frozen package")
	require.NoError(t, m.ExpectationsWereMet())
}

func piReadSnapshotExpect(m sqlmock.Sqlmock, revision int64) {
	m.ExpectQuery(`SELECT instance_id, revision, config_version, config_encrypted`).WithArgs(int64(11), revision).WillReturnRows(sqlmock.NewRows([]string{"instance_id", "revision", "config_version", "config_encrypted"}).AddRow(11, revision, 1, "encrypted"))
	manifest := service.PluginManifest{SchemaVersion: 2, General: &service.PluginGeneralManifest{ConfigVersion: 1, Hooks: []service.PluginHookEntry{{Name: "inspect", Stages: []contract.Stage{contract.Input}, Protocols: []string{"example.text.v1"}}}}}
	data, _ := json.Marshal(manifest)
	cols := []string{"id", "plugin_key", "name", "version", "description", "author", "manifest", "artifact_path", "install_path", "binary_path", "binary_sha256", "signature_status", "state", "config_encrypted", "last_error", "installed_by", "installed_at", "enabled_at", "updated_at"}
	m.ExpectQuery(`(?s)SELECT id, plugin_key.*FROM sub2api_plugin_installations`).WillReturnRows(sqlmock.NewRows(cols).AddRow(3, "com.example.general", "fixture", "0.1.0", "", "", data, "", "", "", stringsRepeatHash(), "unsigned", "incompatible", "", "", nil, time.Now(), nil, time.Now()))
	m.ExpectQuery(`SELECT id, instance_id, group_id, stage`).WillReturnRows(sqlmock.NewRows([]string{"id", "instance_id", "group_id", "stage", "entry", "protocol", "sort_order", "enabled", "can_replace"}))
}
func stringsRepeatHash() string {
	return "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
}
func TestPluginInstanceRepositoryCommitFailure(t *testing.T) {
	r, m := piMock(t)
	m.ExpectBegin()
	m.ExpectQuery(`SELECT.*FROM sub2api_plugin_instances.*FOR UPDATE`).WillReturnRows(piStateRows(4))
	m.ExpectExec(`INSERT INTO sub2api_plugin_instance_revisions`).WillReturnResult(sqlmock.NewResult(0, 1))
	m.ExpectExec(`UPDATE sub2api_plugin_instances`).WillReturnResult(sqlmock.NewResult(0, 1))
	piReadSnapshotExpect(m, 2)
	m.ExpectCommit().WillReturnError(errors.New("commit failed"))
	result, e := r.SaveRevision(context.Background(), 11, 4, "encrypted")
	require.Nil(t, result)
	require.EqualError(t, e, "commit failed")
	require.NoError(t, m.ExpectationsWereMet())
}
func TestPluginInstanceRepositoryInvalidBindingPreservesRows(t *testing.T) {
	r, m := piMock(t)
	m.ExpectBegin()
	m.ExpectQuery(`SELECT.*FROM sub2api_plugin_instances.*FOR UPDATE`).WillReturnRows(piStateRows(4))
	piReadSnapshotExpect(m, 1)
	m.ExpectRollback()
	_, e := r.ReplaceBindings(context.Background(), 11, 4, []service.PluginGroupBinding{{InstanceID: 11, GroupID: 7, Stage: contract.Input, Entry: "unknown", Protocol: "example.text.v1"}})
	require.ErrorIs(t, e, service.ErrPluginInstanceInvalid)
	require.NoError(t, m.ExpectationsWereMet())
}
func TestPluginInstanceRepositoryBindingFailureRollsBack(t *testing.T) {
	r, m := piMock(t)
	m.ExpectBegin()
	m.ExpectQuery(`SELECT.*FROM sub2api_plugin_instances.*FOR UPDATE`).WillReturnRows(piStateRows(4))
	piReadSnapshotExpect(m, 1)
	m.ExpectQuery(`SELECT id FROM groups.*FOR KEY SHARE`).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(7))
	m.ExpectExec(`DELETE FROM sub2api_plugin_group_bindings`).WillReturnResult(sqlmock.NewResult(0, 2))
	m.ExpectExec(`INSERT INTO sub2api_plugin_group_bindings`).WillReturnError(errors.New("binding failed"))
	m.ExpectRollback()
	_, e := r.ReplaceBindings(context.Background(), 11, 4, []service.PluginGroupBinding{{InstanceID: 11, GroupID: 7, Stage: contract.Input, Entry: "inspect", Protocol: "example.text.v1"}})
	require.EqualError(t, e, "binding failed")
	require.NoError(t, m.ExpectationsWereMet())
}
