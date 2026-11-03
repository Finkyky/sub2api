package service

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/lib/pq"
)

var pluginPricingFields = []string{"input_price", "output_price", "cache_write_price", "cache_read_price", "per_request_price"}

func validPluginPricingField(field string) bool { return slices.Contains(pluginPricingFields, field) }
func (b *PluginTaskCapabilityBroker) readPricing(ctx context.Context, scope PluginTaskScope) ([]PluginTaskPricingSnapshot, error) {
	if b.db == nil {
		return nil, ErrPluginTaskCapability
	}
	for _, field := range scope.PricingFields {
		if !validPluginPricingField(field) {
			return nil, ErrPluginTaskCapability
		}
	}
	projection := "id,channel_id"
	if len(scope.PricingFields) > 0 {
		projection += "," + strings.Join(scope.PricingFields, ",")
	}
	query := "SELECT " + projection + " FROM channel_model_pricing WHERE channel_id=ANY($1::bigint[])"
	args := []any{pq.Array(scope.ChannelIDs)}
	if len(scope.PricingIDs) > 0 {
		query += " AND id=ANY($2::bigint[])"
		args = append(args, pq.Array(scope.PricingIDs))
	}
	query += " ORDER BY id LIMIT 129"
	rows, err := b.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]PluginTaskPricingSnapshot, 0)
	for rows.Next() {
		p, e := scanPluginPricingSelected(rows, scope.PricingFields)
		if e != nil {
			return nil, e
		}
		out = append(out, p)
		if len(out) > pluginTaskMaxScopeItems {
			return nil, ErrPluginTaskCapability
		}
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if len(scope.PricingIDs) > 0 && len(out) != len(scope.PricingIDs) {
		return nil, ErrPluginTaskCapability
	}
	return out, nil
}

type pluginPricingScanner interface{ Scan(...any) error }

func scanPluginPricing(row pluginPricingScanner) (PluginTaskPricingSnapshot, error) {
	return scanPluginPricingSelected(row, pluginPricingFields)
}
func scanPluginPricingSelected(row pluginPricingScanner, fields []string) (PluginTaskPricingSnapshot, error) {
	var p PluginTaskPricingSnapshot
	values := make([]sql.NullFloat64, len(fields))
	args := []any{&p.PricingID, &p.ChannelID}
	for i := range values {
		args = append(args, &values[i])
	}
	if err := row.Scan(args...); err != nil {
		return p, err
	}
	p.Fields = make(map[string]*float64, len(fields))
	for i, f := range fields {
		if values[i].Valid {
			v := values[i].Float64
			if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
				return p, ErrPluginTaskInvalid
			}
			p.Fields[f] = &v
		} else {
			p.Fields[f] = nil
		}
	}
	return p, nil
}
func pluginTaskUpdateFields(p PluginTaskPriceUpdate) []string {
	fields := make([]string, 0, len(p.Fields))
	for f := range p.Fields {
		fields = append(fields, f)
	}
	sort.Strings(fields)
	return fields
}
func projectPluginPricing(p PluginTaskPricingSnapshot, fields []string) PluginTaskPricingSnapshot {
	out := PluginTaskPricingSnapshot{ChannelID: p.ChannelID, PricingID: p.PricingID, Fields: make(map[string]*float64, len(fields))}
	for _, field := range fields {
		v := p.Fields[field]
		if v != nil {
			copy := *v
			out.Fields[field] = &copy
		} else {
			out.Fields[field] = nil
		}
	}
	return out
}
func validatePluginTaskMutations(g pluginTaskGrant, out PluginTaskCapabilityOutput) error {
	if len(out.PriceUpdates) > 0 && !slices.Contains(g.capabilities, PluginHostPricingWriteV1) || len(out.KVUpdates) > 0 && !slices.Contains(g.capabilities, PluginHostKVV1) {
		return ErrPluginTaskCapability
	}
	prices := map[int64]bool{}
	for _, p := range out.PriceUpdates {
		if p.ChannelID <= 0 || p.PricingID <= 0 || prices[p.PricingID] || !slices.Contains(g.scope.ChannelIDs, p.ChannelID) || (len(g.scope.PricingIDs) > 0 && !slices.Contains(g.scope.PricingIDs, p.PricingID)) || len(p.Fields) == 0 || len(p.Fields) > 5 {
			return ErrPluginTaskCapability
		}
		prices[p.PricingID] = true
		for field, value := range p.Fields {
			if !validPluginPricingField(field) || !slices.Contains(g.scope.PricingFields, field) {
				return ErrPluginTaskCapability
			}
			// NUMERIC(20,12) has 8 integral digits; per-request (20,10) has 10.
			max := 1e8
			if field == "per_request_price" {
				max = 1e10
			}
			if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value >= max {
				return ErrPluginTaskInvalid
			}
		}
	}
	keys := map[string]bool{}
	for _, kv := range out.KVUpdates {
		if kv.Key == "" || keys[kv.Key] || !slices.Contains(g.scope.KVKeys, kv.Key) {
			return ErrPluginTaskCapability
		}
		keys[kv.Key] = true
	}
	return nil
}

// The caller owns fencing and final COMMIT. This savepoint prevents a caller
// that handles an error and still commits from retaining ANY partial mutations.
// No SQL identifier or expression comes from plugin data: fields are whitelisted.
func commitPluginTaskMutations(ctx context.Context, tx *sql.Tx, c *PluginTaskClaim, g pluginTaskGrant, out PluginTaskCapabilityOutput) (err error) {
	if len(out.PriceUpdates) == 0 && len(out.KVUpdates) == 0 {
		return nil
	}
	if _, err = tx.ExecContext(ctx, `SAVEPOINT plugin_task_capability`); err != nil {
		return err
	}
	defer func() {
		if err != nil {
			// Use a fresh bounded context if the operation context expired. Roll back
			// only our savepoint, preserving caller fencing updates and transaction.
			cleanup, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if _, e := tx.ExecContext(cleanup, `ROLLBACK TO SAVEPOINT plugin_task_capability`); e != nil {
				err = fmt.Errorf("%w; capability rollback: %v", err, e)
				return
			}
			_, _ = tx.ExecContext(cleanup, `RELEASE SAVEPOINT plugin_task_capability`)
		}
	}()
	// Serialize this instance's KV/idempotency operations across brokers/processes.
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, c.Task.InstanceID); err != nil {
		return err
	}
	data, e := json.Marshal(out)
	if e != nil {
		return e
	}
	digest := sha256.Sum256(data)
	hash := hex.EncodeToString(digest[:])
	var previous string
	var taskID int64
	var plugin, entry string
	e = tx.QueryRowContext(ctx, `SELECT output_hash,task_id,source_plugin,source_entry FROM sub2api_plugin_task_capability_commits WHERE instance_id=$1 AND task_id=$2 AND idempotency_key=$3`, c.Task.InstanceID, c.Task.ID, c.Run.IdempotencyKey).Scan(&previous, &taskID, &plugin, &entry)
	if e == nil {
		if previous != hash || taskID != c.Task.ID || plugin != g.plugin || entry != g.entry {
			return ErrPluginTaskConflict
		}
		_, err = tx.ExecContext(ctx, `RELEASE SAVEPOINT plugin_task_capability`)
		return err
	}
	if e != sql.ErrNoRows {
		return e
	}
	// Lock in deterministic pricing ID order and prevalidate EVERY database row
	// before writing either pricing, history, receipt, or KV.
	updates := slices.Clone(out.PriceUpdates)
	sort.Slice(updates, func(i, j int) bool { return updates[i].PricingID < updates[j].PricingID })
	old := make([]PluginTaskPricingSnapshot, len(updates))
	for i, p := range updates {
		fields := pluginTaskUpdateFields(p)
		query := "SELECT id,channel_id," + strings.Join(fields, ",") + " FROM channel_model_pricing WHERE id=$1 AND channel_id=$2 FOR UPDATE"
		old[i], err = scanPluginPricingSelected(tx.QueryRowContext(ctx, query, p.PricingID, p.ChannelID), fields)
		if err == sql.ErrNoRows {
			return ErrPluginTaskCapability
		}
		if err != nil {
			return err
		}
	}
	// Values cannot be invalid at this point. A subsequent SQL error is still
	// contained by the savepoint, including constraint/audit/KV write failures.
	for i, p := range updates {
		fields := pluginTaskUpdateFields(p)
		set := make([]string, 0, len(fields))
		args := make([]any, 0, len(fields)+2)
		for _, field := range fields {
			args = append(args, p.Fields[field])
			set = append(set, fmt.Sprintf("%s=$%d", field, len(args)))
		}
		args = append(args, p.PricingID, p.ChannelID)
		query := fmt.Sprintf(`UPDATE channel_model_pricing SET %s,updated_at=NOW() WHERE id=$%d AND channel_id=$%d RETURNING id,channel_id,%s`, strings.Join(set, ","), len(args)-1, len(args), strings.Join(fields, ","))
		next, e := scanPluginPricingSelected(tx.QueryRowContext(ctx, query, args...), fields)
		if e != nil {
			return e
		}
		// History captures the database's rounded NUMERIC values, not an unrounded
		// output float. Store only the explicit changed fields with their old values.
		oldJSON, _ := json.Marshal(projectPluginPricing(old[i], fields).Fields)
		newJSON, _ := json.Marshal(projectPluginPricing(next, fields).Fields)
		_, err = tx.ExecContext(ctx, `INSERT INTO sub2api_plugin_pricing_versions (run_id,instance_id,task_id,idempotency_key,channel_id,pricing_id,old_values,new_values,source_plugin,source_version,source_entry) VALUES ($1,$2,$3,$4,$5,$6,$7::jsonb,$8::jsonb,$9,$10,$11)`, c.Run.ID, c.Task.InstanceID, c.Task.ID, c.Run.IdempotencyKey, p.ChannelID, p.PricingID, string(oldJSON), string(newJSON), g.plugin, g.version, g.entry)
		if err != nil {
			return err
		}
	}
	for _, kv := range out.KVUpdates {
		if kv.Delete {
			_, err = tx.ExecContext(ctx, `DELETE FROM sub2api_plugin_task_kv WHERE instance_id=$1 AND key=$2`, c.Task.InstanceID, kv.Key)
		} else {
			_, err = tx.ExecContext(ctx, `INSERT INTO sub2api_plugin_task_kv (instance_id,key,value) VALUES ($1,$2,$3::jsonb) ON CONFLICT (instance_id,key) DO UPDATE SET value=EXCLUDED.value,updated_at=NOW()`, c.Task.InstanceID, kv.Key, string(kv.Value))
		}
		if err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO sub2api_plugin_task_capability_commits (instance_id,idempotency_key,run_id,task_id,output_hash,source_plugin,source_entry) VALUES ($1,$2,$3,$4,$5,$6,$7)`, c.Task.InstanceID, c.Run.IdempotencyKey, c.Run.ID, c.Task.ID, hash, g.plugin, g.entry)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `RELEASE SAVEPOINT plugin_task_capability`)
	return err
}

type PluginPricingVersion struct {
	ID             int64               `json:"id"`
	TaskID         int64               `json:"task_id"`
	RunID          int64               `json:"run_id"`
	InstanceID     int64               `json:"instance_id"`
	IdempotencyKey string              `json:"idempotency_key"`
	ChannelID      int64               `json:"channel_id"`
	PricingID      int64               `json:"pricing_id"`
	OldValues      map[string]*float64 `json:"old_values"`
	NewValues      map[string]*float64 `json:"new_values"`
	SourcePlugin   string              `json:"source_plugin"`
	SourceVersion  string              `json:"source_version"`
	SourceEntry    string              `json:"source_entry"`
	CreatedAt      time.Time           `json:"created_at"`
}

// PricingHistory is a bounded internal query for assembly by an authorized
// administration service. It is not exposed through a route by this change.
func (b *PluginTaskCapabilityBroker) PricingHistory(ctx context.Context, channelID, pricingID int64, limit int) ([]PluginPricingVersion, error) {
	if b.db == nil || channelID <= 0 || pricingID <= 0 || limit < 1 || limit > 200 {
		return nil, ErrPluginTaskInvalid
	}
	rows, err := b.db.QueryContext(ctx, `SELECT id,run_id,instance_id,task_id,idempotency_key,channel_id,pricing_id,old_values,new_values,source_plugin,source_version,source_entry,created_at FROM sub2api_plugin_pricing_versions WHERE channel_id=$1 AND pricing_id=$2 ORDER BY id DESC LIMIT $3`, channelID, pricingID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]PluginPricingVersion, 0)
	for rows.Next() {
		var v PluginPricingVersion
		var old, next []byte
		if err = rows.Scan(&v.ID, &v.RunID, &v.InstanceID, &v.TaskID, &v.IdempotencyKey, &v.ChannelID, &v.PricingID, &old, &next, &v.SourcePlugin, &v.SourceVersion, &v.SourceEntry, &v.CreatedAt); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(old, &v.OldValues); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(next, &v.NewValues); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
