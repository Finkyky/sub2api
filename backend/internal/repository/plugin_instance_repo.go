package repository

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
	contract "github.com/Wei-Shaw/sub2api/pkg/pluginapi/contracts/v1"
)

type pluginInstanceRepository struct{ db *sql.DB }

func NewPluginInstanceRepository(db *sql.DB) service.PluginInstanceRepository {
	return &pluginInstanceRepository{db: db}
}

const pluginInstanceSelect = `SELECT id, plugin_id, name, generation, current_revision, latest_revision, enabled FROM sub2api_plugin_instances`

func scanPluginInstance(row pluginScanner) (service.PluginInstance, error) {
	var i service.PluginInstance
	e := row.Scan(&i.ID, &i.PluginID, &i.Name, &i.Generation, &i.CurrentRevision, &i.LatestRevision, &i.Enabled)
	if errors.Is(e, sql.ErrNoRows) {
		e = service.ErrPluginInstanceNotFound
	}
	return i, e
}
func readPluginInstance(ctx context.Context, tx *sql.Tx, id int64, lock bool) (service.PluginInstance, error) {
	q := pluginInstanceSelect + ` WHERE id = $1`
	if lock {
		q += ` FOR UPDATE`
	}
	return scanPluginInstance(tx.QueryRowContext(ctx, q, id))
}
func readPluginInstanceSnapshot(ctx context.Context, tx *sql.Tx, i service.PluginInstance, revision int64) (*service.PluginInstanceSnapshot, error) {
	if revision == 0 {
		revision = i.CurrentRevision
	}
	s := &service.PluginInstanceSnapshot{Instance: i, Bindings: make([]service.PluginGroupBinding, 0)}
	e := tx.QueryRowContext(ctx, `SELECT instance_id, revision, config_version, config_encrypted FROM sub2api_plugin_instance_revisions WHERE instance_id=$1 AND revision=$2`, i.ID, revision).Scan(&s.Revision.InstanceID, &s.Revision.Revision, &s.Revision.ConfigVersion, &s.Revision.ConfigEncrypted)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, service.ErrPluginInstanceNotFound
	}
	if e != nil {
		return nil, e
	}
	p, e := scanPlugin(tx.QueryRowContext(ctx, pluginSelectSQL+` WHERE id=$1`, i.PluginID))
	if e != nil {
		return nil, e
	}
	s.Installation = *p
	rows, e := tx.QueryContext(ctx, `SELECT id, instance_id, group_id, stage, entry, protocol, sort_order, enabled, can_replace FROM sub2api_plugin_group_bindings WHERE instance_id=$1 ORDER BY sort_order, id`, i.ID)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	for rows.Next() {
		var b service.PluginGroupBinding
		if e = rows.Scan(&b.ID, &b.InstanceID, &b.GroupID, &b.Stage, &b.Entry, &b.Protocol, &b.Order, &b.Enabled, &b.CanReplace); e != nil {
			return nil, e
		}
		s.Bindings = append(s.Bindings, b)
	}
	return s, rows.Err()
}
func commitPluginInstance(ctx context.Context, tx *sql.Tx, i service.PluginInstance, revision int64) (*service.PluginInstanceSnapshot, error) {
	s, e := readPluginInstanceSnapshot(ctx, tx, i, revision)
	if e != nil {
		return nil, e
	}
	if e = tx.Commit(); e != nil {
		return nil, e
	}
	return s, nil
}
func checkPluginInstanceGeneration(i service.PluginInstance, expected int64) error {
	if expected <= 0 || i.Generation != expected {
		return service.ErrPluginInstanceConflict
	}
	return nil
}
func (r *pluginInstanceRepository) Create(ctx context.Context, pluginID int64, name, encrypted string) (*service.PluginInstanceSnapshot, error) {
	if e := service.ValidatePluginInstanceCreate(pluginID, name, encrypted); e != nil {
		return nil, e
	}
	tx, e := r.db.BeginTx(ctx, nil)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	// Serialize against installation upsert/delete. An instance freezes this package.
	p, e := scanPlugin(tx.QueryRowContext(ctx, pluginSelectSQL+` WHERE id=$1 FOR UPDATE`, pluginID))
	if e != nil {
		return nil, e
	}
	if p.Manifest.SchemaVersion != 2 || p.Manifest.General == nil || p.Manifest.Validate() != nil || p.PluginKey != p.Manifest.ID || p.Version != p.Manifest.Version {
		return nil, service.ErrPluginInstanceInvalid
	}
	var id int64
	e = tx.QueryRowContext(ctx, `INSERT INTO sub2api_plugin_instances(plugin_id,name) VALUES($1,$2) RETURNING id`, pluginID, strings.TrimSpace(name)).Scan(&id)
	if e != nil {
		return nil, e
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO sub2api_plugin_instance_revisions(instance_id,revision,config_version,config_encrypted) VALUES($1,1,$2,$3)`, id, p.Manifest.General.ConfigVersion, encrypted)
	if e != nil {
		return nil, e
	}
	return commitPluginInstance(ctx, tx, service.PluginInstance{ID: id, PluginID: pluginID, Name: strings.TrimSpace(name), Generation: 1, CurrentRevision: 1, LatestRevision: 1}, 1)
}
func (r *pluginInstanceRepository) List(ctx context.Context, pluginID int64) ([]service.PluginInstance, error) {
	if pluginID < 0 {
		return nil, service.ErrPluginInstanceInvalid
	}
	query := pluginInstanceSelect
	args := []any{}
	if pluginID > 0 {
		query += ` WHERE plugin_id = $1`
		args = append(args, pluginID)
	}
	query += ` ORDER BY id`
	rows, e := r.db.QueryContext(ctx, query, args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	instances := make([]service.PluginInstance, 0)
	for rows.Next() {
		var i service.PluginInstance
		if e = rows.Scan(&i.ID, &i.PluginID, &i.Name, &i.Generation, &i.CurrentRevision, &i.LatestRevision, &i.Enabled); e != nil {
			return nil, e
		}
		instances = append(instances, i)
	}
	if e = rows.Err(); e != nil {
		return nil, e
	}
	return instances, nil
}

func (r *pluginInstanceRepository) Snapshot(ctx context.Context, id, revision int64) (*service.PluginInstanceSnapshot, error) {
	if id <= 0 || revision < 0 {
		return nil, service.ErrPluginInstanceInvalid
	}
	tx, e := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	i, e := readPluginInstance(ctx, tx, id, false)
	if e != nil {
		return nil, e
	}
	return commitPluginInstance(ctx, tx, i, revision)
}
func (r *pluginInstanceRepository) RevisionSummaries(ctx context.Context, instanceID int64) ([]service.PluginInstanceRevisionSummary, error) {
	if instanceID <= 0 {
		return nil, service.ErrPluginInstanceInvalid
	}
	rows, e := r.db.QueryContext(ctx, `SELECT instance_id, revision, config_version FROM sub2api_plugin_instance_revisions WHERE instance_id = $1 ORDER BY revision`, instanceID)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	revisions := make([]service.PluginInstanceRevisionSummary, 0)
	for rows.Next() {
		var revision service.PluginInstanceRevisionSummary
		if e = rows.Scan(&revision.InstanceID, &revision.Revision, &revision.ConfigVersion); e != nil {
			return nil, e
		}
		revisions = append(revisions, revision)
	}
	if e = rows.Err(); e != nil {
		return nil, e
	}
	// Every live instance owns at least its first immutable revision. An empty
	// result therefore means the instance is absent, including after deletion.
	if len(revisions) == 0 {
		return nil, service.ErrPluginInstanceNotFound
	}
	return revisions, nil
}

func (r *pluginInstanceRepository) SaveRevision(ctx context.Context, id, expected int64, encrypted string) (*service.PluginInstanceSnapshot, error) {
	if id <= 0 || encrypted == "" || len(encrypted) > 256*1024 {
		return nil, service.ErrPluginInstanceInvalid
	}
	tx, e := r.db.BeginTx(ctx, nil)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	i, e := readPluginInstance(ctx, tx, id, true)
	if e != nil {
		return nil, e
	}
	if e = checkPluginInstanceGeneration(i, expected); e != nil {
		return nil, e
	}
	revision := i.LatestRevision + 1
	_, e = tx.ExecContext(ctx, `INSERT INTO sub2api_plugin_instance_revisions(instance_id,revision,config_version,config_encrypted) SELECT instance_id,$2,config_version,$3 FROM sub2api_plugin_instance_revisions WHERE instance_id=$1 AND revision=1`, id, revision, encrypted)
	if e != nil {
		return nil, e
	}
	if e = writePluginInstanceState(ctx, tx, id, i.Generation+1, i.CurrentRevision, revision, i.Enabled); e != nil {
		return nil, e
	}
	i.Generation++
	i.LatestRevision = revision
	return commitPluginInstance(ctx, tx, i, revision)
}
func writePluginInstanceState(ctx context.Context, tx *sql.Tx, id, generation, current, latest int64, enabled bool) error {
	result, e := tx.ExecContext(ctx, `UPDATE sub2api_plugin_instances SET generation=$2,current_revision=$3,latest_revision=$4,enabled=$5,updated_at=NOW() WHERE id=$1`, id, generation, current, latest, enabled)
	if e != nil {
		return e
	}
	n, e := result.RowsAffected()
	if e != nil {
		return e
	}
	if n != 1 {
		return service.ErrPluginInstanceConflict
	}
	return nil
}
func (r *pluginInstanceRepository) Publish(ctx context.Context, id, expected, revision int64, enabled bool) (*service.PluginInstanceSnapshot, error) {
	if id <= 0 || revision <= 0 {
		return nil, service.ErrPluginInstanceInvalid
	}
	tx, e := r.db.BeginTx(ctx, nil)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	i, e := readPluginInstance(ctx, tx, id, true)
	if e != nil {
		return nil, e
	}
	if e = checkPluginInstanceGeneration(i, expected); e != nil {
		return nil, e
	}
	s, e := readPluginInstanceSnapshot(ctx, tx, i, revision)
	if e != nil {
		return nil, e
	}
	if e = service.ValidatePluginGroupBindings(s.Installation.Manifest, id, s.Bindings); e != nil {
		return nil, e
	}
	if e = writePluginInstanceState(ctx, tx, id, i.Generation+1, revision, i.LatestRevision, enabled); e != nil {
		return nil, e
	}
	s.Instance.Generation++
	s.Instance.CurrentRevision = revision
	s.Instance.Enabled = enabled
	if e = tx.Commit(); e != nil {
		return nil, e
	}
	return s, nil
}
func (r *pluginInstanceRepository) ReplaceBindings(ctx context.Context, id, expected int64, bindings []service.PluginGroupBinding) (*service.PluginInstanceSnapshot, error) {
	if id <= 0 {
		return nil, service.ErrPluginInstanceInvalid
	}
	tx, e := r.db.BeginTx(ctx, nil)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	i, e := readPluginInstance(ctx, tx, id, true)
	if e != nil {
		return nil, e
	}
	if e = checkPluginInstanceGeneration(i, expected); e != nil {
		return nil, e
	}
	s, e := readPluginInstanceSnapshot(ctx, tx, i, 0)
	if e != nil {
		return nil, e
	}
	if e = service.ValidatePluginGroupBindings(s.Installation.Manifest, id, bindings); e != nil {
		return nil, e
	}
	// Lock group IDs in numeric order to avoid cross-instance binding deadlocks.
	groups := make(map[int64]bool)
	ids := make([]int64, 0)
	for _, b := range bindings {
		if !groups[b.GroupID] {
			groups[b.GroupID] = true
			ids = append(ids, b.GroupID)
		}
	}
	slices.Sort(ids)
	for _, groupID := range ids {
		var found int64
		e = tx.QueryRowContext(ctx, `SELECT id FROM groups WHERE id=$1 AND deleted_at IS NULL FOR KEY SHARE`, groupID).Scan(&found)
		if errors.Is(e, sql.ErrNoRows) {
			return nil, service.ErrPluginInstanceInvalid
		}
		if e != nil {
			return nil, e
		}
	}
	if _, e = tx.ExecContext(ctx, `DELETE FROM sub2api_plugin_group_bindings WHERE instance_id=$1`, id); e != nil {
		return nil, e
	}
	for _, b := range bindings {
		_, e = tx.ExecContext(ctx, `INSERT INTO sub2api_plugin_group_bindings(instance_id,group_id,stage,entry,protocol,sort_order,enabled,can_replace) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, id, b.GroupID, b.Stage, b.Entry, b.Protocol, b.Order, b.Enabled, b.CanReplace)
		if e != nil {
			return nil, e
		}
	}
	if e = writePluginInstanceState(ctx, tx, id, i.Generation+1, i.CurrentRevision, i.LatestRevision, i.Enabled); e != nil {
		return nil, e
	}
	i.Generation++
	return commitPluginInstance(ctx, tx, i, 0)
}
func (r *pluginInstanceRepository) GroupSnapshots(ctx context.Context, groupID int64, stage contract.Stage, protocol string) ([]service.PluginInstanceSnapshot, error) {
	if groupID <= 0 || !stage.Valid() || strings.TrimSpace(protocol) == "" {
		return nil, service.ErrPluginInstanceInvalid
	}
	tx, e := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	// Do not filter disabled instances: a live binding must fail closed.
	rows, e := tx.QueryContext(ctx, `SELECT DISTINCT instance_id FROM sub2api_plugin_group_bindings WHERE group_id=$1 AND stage=$2 AND enabled=TRUE ORDER BY instance_id`, groupID, stage)
	if e != nil {
		return nil, e
	}
	ids := make([]int64, 0)
	for rows.Next() {
		var id int64
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return nil, e
		}
		ids = append(ids, id)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	result := make([]service.PluginInstanceSnapshot, 0, len(ids))
	for _, id := range ids {
		i, e := readPluginInstance(ctx, tx, id, false)
		if e != nil {
			return nil, e
		}
		s, e := readPluginInstanceSnapshot(ctx, tx, i, 0)
		if e != nil {
			return nil, e
		}
		selected := make([]service.PluginGroupBinding, 0)
		// An entry is a required execution step; protocol rows of the same
		// entry are alternatives. Another instance cannot mask a missing step.
		entries := make(map[string]bool)
		for _, b := range s.Bindings {
			if b.Enabled && b.GroupID == groupID && b.Stage == stage {
				if _, ok := entries[b.Entry]; !ok {
					entries[b.Entry] = false
				}
				if b.Protocol == protocol {
					entries[b.Entry] = true
					selected = append(selected, b)
				}
			}
		}
		for _, matched := range entries {
			if !matched {
				return nil, service.ErrPluginInstanceUnavailable
			}
		}
		s.Bindings = selected
		result = append(result, *s)
	}
	if e = tx.Commit(); e != nil {
		return nil, e
	}
	return result, nil
}

// Delete atomically validates authority before deleting the instance. Migration
// 242 cascades both bindings and immutable revisions in this transaction; its
// deferred current-revision FK also permits deletion of the whole aggregate.
func (r *pluginInstanceRepository) Delete(ctx context.Context, id, expected int64) error {
	if id <= 0 || expected <= 0 {
		return service.ErrPluginInstanceInvalid
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	instance, err := readPluginInstance(ctx, tx, id, true)
	if err != nil {
		return err
	}
	if err = checkPluginInstanceGeneration(instance, expected); err != nil {
		return err
	}
	if instance.Enabled {
		return service.ErrPluginInstanceEnabled
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM sub2api_plugin_instances WHERE id=$1`, id)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return service.ErrPluginInstanceConflict
	}
	return tx.Commit()
}
