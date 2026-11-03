package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	contract "github.com/Wei-Shaw/sub2api/pkg/pluginapi/contracts/v1"
)

const (
	PluginHostKVV1                    = "host.kv.v1"
	PluginHostNetworkReadV1           = "host.network.read.v1"
	PluginHostPricingReadV1           = "host.channel.pricing.read.v1"
	PluginHostPricingWriteV1          = "host.channel.pricing.write.v1"
	PluginTaskCapabilityOutputLimit   = 64 * 1024
	PluginTaskCapabilityEnvelopeLimit = 64 * 1024
	PluginTaskCapabilityKVLimit       = 16 * 1024
	pluginTaskMaxScopeItems           = 128
	pluginTaskMaxPrepared             = 1024
)

// All output is sensitive, including summary and arbitrary KV values. The task
// executor must encrypt the original TaskResult.Output before persisting it.
// The broker stores only the explicit authorized mutations, never raw output.
type PluginTaskCapabilityOutput struct {
	Summary      string                  `json:"summary,omitempty"`
	PriceUpdates []PluginTaskPriceUpdate `json:"price_updates,omitempty"`
	KVUpdates    []PluginTaskKVUpdate    `json:"kv_updates,omitempty"`
}
type PluginTaskPriceUpdate struct {
	ChannelID int64              `json:"channel_id"`
	PricingID int64              `json:"pricing_id"`
	Fields    map[string]float64 `json:"fields"`
}
type PluginTaskKVUpdate struct {
	Key    string          `json:"key"`
	Value  json.RawMessage `json:"value,omitempty"`
	Delete bool            `json:"delete,omitempty"`
}
type PluginTaskPricingSnapshot struct {
	ChannelID int64               `json:"channel_id"`
	PricingID int64               `json:"pricing_id"`
	Fields    map[string]*float64 `json:"fields"`
}
type pluginTaskNetworkSnapshot struct {
	URL  string          `json:"url"`
	Body json.RawMessage `json:"body"`
}
type pluginTaskEnvelope struct {
	Input json.RawMessage `json:"input"`
	Host  struct {
		KV      map[string]json.RawMessage  `json:"kv"`
		Network []pluginTaskNetworkSnapshot `json:"network"`
		Pricing []PluginTaskPricingSnapshot `json:"pricing"`
	} `json:"host"`
}
type pluginTaskGrant struct {
	fingerprint  [32]byte
	scope        PluginTaskScope
	capabilities []string
	plugin       string
	version      string
	entry        string
	expires      time.Time
}
type pluginTaskGrantKey struct {
	run, fence int64
	attempt    int
}

// Prepare and Commit must use the same broker and unchanged fenced claim.
// Grants are bounded, local, immutable projections of the administrator scope;
// manifest declarations alone never cause a host read or authorize a write.
type PluginTaskCapabilityBroker struct {
	db       *sql.DB
	channels *ChannelService
	mu       sync.Mutex
	grants   map[pluginTaskGrantKey]pluginTaskGrant
	lookupIP func(context.Context, string) ([]net.IPAddr, error)
	// Unexported test seam: production construction never installs a transport
	// override. URL and DNS policy still run before this RoundTripper.
	testRoundTripper http.RoundTripper
}

// PluginTaskCapabilityDiscarder is optional so the frozen PluginTaskBroker
// interface remains unchanged. Executor defers Discard immediately after Claim,
// including Prepare failure, cancellation, runtime failure and fencing rejection.
type PluginTaskCapabilityDiscarder interface{ Discard(*PluginTaskClaim) }

func (b *PluginTaskCapabilityBroker) Discard(c *PluginTaskClaim) {
	if c == nil {
		return
	}
	b.mu.Lock()
	delete(b.grants, pluginTaskClaimKey(c))
	b.mu.Unlock()
}

var _ PluginTaskBroker = (*PluginTaskCapabilityBroker)(nil)

func NewPluginTaskCapabilityBroker(db *sql.DB, channels *ChannelService) *PluginTaskCapabilityBroker {
	return &PluginTaskCapabilityBroker{db: db, channels: channels, grants: make(map[pluginTaskGrantKey]pluginTaskGrant), lookupIP: net.DefaultResolver.LookupIPAddr}
}
func pluginTaskClaimFingerprint(c *PluginTaskClaim) ([32]byte, error) {
	if c == nil {
		return [32]byte{}, ErrPluginTaskCapability
	}
	raw, err := json.Marshal(c)
	return sha256.Sum256(raw), err
}
func pluginTaskClaimKey(c *PluginTaskClaim) pluginTaskGrantKey {
	return pluginTaskGrantKey{c.Run.ID, c.Run.FencingToken, c.Run.Attempt}
}
func (b *PluginTaskCapabilityBroker) Prepare(ctx context.Context, c *PluginTaskClaim, s *PluginInstanceSnapshot) (json.RawMessage, error) {
	// Re-preparing revokes the old projection even if validation or a host read fails.
	b.Discard(c)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	grant, err := validatePluginTaskGrant(c, s)
	if err != nil {
		return nil, err
	}
	var env pluginTaskEnvelope
	env.Input = append(json.RawMessage(nil), c.Task.Input...)
	if len(env.Input) == 0 {
		env.Input = json.RawMessage(`null`)
	}
	if !json.Valid(env.Input) || len(env.Input) > PluginTaskCapabilityEnvelopeLimit {
		return nil, ErrPluginTaskInvalid
	}
	env.Host.KV = make(map[string]json.RawMessage)
	env.Host.Network = make([]pluginTaskNetworkSnapshot, 0)
	env.Host.Pricing = make([]PluginTaskPricingSnapshot, 0)
	if len(grant.scope.KVKeys) > 0 {
		if b.db == nil {
			return nil, ErrPluginTaskCapability
		}
		for _, key := range grant.scope.KVKeys {
			var raw []byte
			err = b.db.QueryRowContext(ctx, `SELECT value FROM sub2api_plugin_task_kv WHERE instance_id=$1 AND key=$2`, c.Task.InstanceID, key).Scan(&raw)
			if err == sql.ErrNoRows {
				env.Host.KV[key] = json.RawMessage(`null`)
				continue
			}
			if err != nil {
				return nil, err
			}
			if len(raw) > 2*PluginTaskCapabilityKVLimit || !json.Valid(raw) {
				return nil, ErrPluginTaskInvalid
			}
			env.Host.KV[key] = append(json.RawMessage(nil), raw...)
		}
	}
	if (slices.Contains(grant.capabilities, PluginHostPricingReadV1) && len(grant.scope.ChannelIDs) > 0 && len(grant.scope.PricingFields) > 0) || len(grant.scope.PricingIDs) > 0 {
		pricingScope := grant.scope
		if !slices.Contains(grant.capabilities, PluginHostPricingReadV1) {
			pricingScope.PricingFields = nil
		}
		pricing, readErr := b.readPricing(ctx, pricingScope)
		if readErr != nil {
			return nil, readErr
		}
		if slices.Contains(grant.capabilities, PluginHostPricingReadV1) && len(grant.scope.PricingFields) > 0 {
			env.Host.Pricing = pricing
		}
	}
	for _, url := range grant.scope.NetworkURLs {
		body, e := b.readNetwork(ctx, url)
		if e != nil {
			return nil, e
		}
		env.Host.Network = append(env.Host.Network, pluginTaskNetworkSnapshot{URL: url, Body: body})
	}
	raw, err := json.Marshal(env)
	if err != nil {
		return nil, err
	}
	if len(raw) > PluginTaskCapabilityEnvelopeLimit {
		return nil, ErrPluginTaskInvalid
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for key, g := range b.grants {
		if time.Now().After(g.expires) {
			delete(b.grants, key)
		}
	}
	if len(b.grants) >= pluginTaskMaxPrepared {
		return nil, ErrPluginTaskCapability
	}
	b.grants[pluginTaskClaimKey(c)] = grant
	return raw, nil
}
func validatePluginTaskGrant(c *PluginTaskClaim, s *PluginInstanceSnapshot) (pluginTaskGrant, error) {
	var g pluginTaskGrant
	if c == nil || s == nil || c.Task.ID <= 0 || c.Task.InstanceID <= 0 || c.Run.ID <= 0 || c.Run.TaskID != c.Task.ID || c.Run.InstanceID != c.Task.InstanceID || s.Instance.ID != c.Task.InstanceID || c.Task.Entry == "" || c.Run.FencingToken <= 0 || c.Run.IdempotencyKey == "" || len(c.Run.IdempotencyKey) > 256 {
		return g, ErrPluginTaskCapability
	}
	if c.Run.ConfigRevision <= 0 || s.Revision.InstanceID != c.Task.InstanceID || s.Revision.Revision != c.Run.ConfigRevision || c.InstanceGeneration <= 0 || s.Instance.Generation != c.InstanceGeneration || !s.Instance.Enabled || c.Run.TaskGeneration != c.Task.Generation {
		return g, ErrPluginTaskCapability
	}
	m := s.Installation.Manifest
	if len(s.Installation.Bindings) > 0 {
		return g, ErrPluginTaskCapability
	}
	if err := ValidatePluginTaskCapabilities(c.Task, m); err != nil {
		return g, err
	}
	scope := c.Task.Scope
	var fingerprintErr error
	g.fingerprint, fingerprintErr = pluginTaskClaimFingerprint(c)
	if fingerprintErr != nil {
		return g, ErrPluginTaskInvalid
	}
	g.scope = PluginTaskScope{PricingIDs: slices.Clone(scope.PricingIDs), ChannelIDs: slices.Clone(scope.ChannelIDs), PricingFields: slices.Clone(scope.PricingFields), NetworkURLs: slices.Clone(scope.NetworkURLs), KVKeys: slices.Clone(scope.KVKeys)}
	g.capabilities = slices.Clone(m.General.HostCapabilities)
	g.plugin = m.ID
	g.version = m.Version
	g.entry = c.Task.Entry
	g.expires = time.Now().Add(10 * time.Minute)
	return g, nil
}
func (b *PluginTaskCapabilityBroker) Commit(ctx context.Context, tx *sql.Tx, c *PluginTaskClaim, result contract.TaskResult) error {
	if tx == nil || c == nil {
		return ErrPluginTaskCapability
	}
	fp, err := pluginTaskClaimFingerprint(c)
	if err != nil {
		return ErrPluginTaskCapability
	}
	b.mu.Lock()
	g, ok := b.grants[pluginTaskClaimKey(c)]
	b.mu.Unlock()
	if !ok || g.fingerprint != fp || time.Now().After(g.expires) {
		return ErrPluginTaskCapability
	}
	if err = contract.ValidateTaskResult(result); err != nil {
		return err
	}
	// A failed plugin never applies output, even if it claims Changed=true.
	if result.Status != contract.TaskSucceeded {
		return nil
	}
	out, err := decodePluginTaskOutput(result.Output)
	if err != nil {
		return err
	}
	if err = validatePluginTaskMutations(g, out); err != nil {
		return err
	}
	return commitPluginTaskMutations(ctx, tx, c, g, out)
}

// Caller invokes only after its fenced Finish transaction has committed. Cache
// invalidation replaces the cache, never mutates previously acquired objects.
func (b *PluginTaskCapabilityBroker) AfterCommit() {
	if b.channels != nil {
		b.channels.InvalidateCache()
	}
}

func decodePluginTaskOutput(raw json.RawMessage) (PluginTaskCapabilityOutput, error) {
	var out PluginTaskCapabilityOutput
	if len(raw) == 0 {
		return out, nil
	}
	if len(raw) > PluginTaskCapabilityOutputLimit || !json.Valid(raw) {
		return out, ErrPluginTaskInvalid
	}
	if err := rejectPluginTaskDuplicateKeys(raw); err != nil {
		return out, err
	}
	var shape map[string]json.RawMessage
	if err := json.Unmarshal(raw, &shape); err != nil || shape == nil {
		return out, ErrPluginTaskInvalid
	}
	for key, v := range shape {
		if bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
			return out, ErrPluginTaskInvalid
		}
		if key != "summary" && key != "price_updates" && key != "kv_updates" {
			return out, ErrPluginTaskInvalid
		}
	}
	// A null float is accepted as zero by encoding/json; reject it explicitly.
	var prices []struct {
		Fields map[string]json.RawMessage `json:"fields"`
	}
	if v := shape["price_updates"]; v != nil {
		if json.Unmarshal(v, &prices) != nil {
			return out, ErrPluginTaskInvalid
		}
		for _, p := range prices {
			for _, value := range p.Fields {
				if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
					return out, ErrPluginTaskInvalid
				}
			}
		}
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&out); err != nil {
		return out, ErrPluginTaskInvalid
	}
	if len(out.Summary) > 4096 || len(out.PriceUpdates) > pluginTaskMaxScopeItems || len(out.KVUpdates) > pluginTaskMaxScopeItems {
		return out, ErrPluginTaskInvalid
	}
	for _, kv := range out.KVUpdates {
		if kv.Delete && len(kv.Value) > 0 || !kv.Delete && (len(kv.Value) == 0 || !json.Valid(kv.Value)) || len(kv.Value) > PluginTaskCapabilityKVLimit {
			return out, ErrPluginTaskInvalid
		}
	}
	return out, nil
}

// Duplicate keys anywhere are ambiguous at the authorization boundary.
func rejectPluginTaskDuplicateKeys(raw []byte) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var walk func(int) error
	walk = func(depth int) error {
		if depth > 64 {
			return ErrPluginTaskInvalid
		}
		tok, err := dec.Token()
		if err != nil {
			return ErrPluginTaskInvalid
		}
		d, ok := tok.(json.Delim)
		if !ok {
			return nil
		}
		switch d {
		case '{':
			seen := map[string]bool{}
			for dec.More() {
				k, err := dec.Token()
				if err != nil {
					return ErrPluginTaskInvalid
				}
				key, ok := k.(string)
				if !ok || seen[key] {
					return ErrPluginTaskInvalid
				}
				seen[key] = true
				if err = walk(depth + 1); err != nil {
					return err
				}
			}
		case '[':
			for dec.More() {
				if err := walk(depth + 1); err != nil {
					return err
				}
			}
		default:
			return ErrPluginTaskInvalid
		}
		_, err = dec.Token()
		return err
	}
	if err := walk(0); err != nil {
		return err
	}
	if _, err := dec.Token(); err != io.EOF {
		return fmt.Errorf("%w: trailing output", ErrPluginTaskInvalid)
	}
	return nil
}

// ValidatePluginTaskCapabilities is the static CRUD counterpart of Prepare.
// Scope is an administrator grant; manifest is only a declaration. This function
// neither resolves DNS nor reads a database, and needs no real run or lease.
// Prepare additionally binds the instance/revision/fence and verifies pricing
// IDs against existing rows and live public DNS immediately before host I/O.
func ValidatePluginTaskCapabilities(task PluginTask, m PluginManifest) error {
	if m.SchemaVersion != 2 || m.General == nil || m.Requires.TransportAPI != 0 {
		return ErrPluginTaskCapability
	}
	taskCap := false
	for _, cap := range m.Capabilities {
		if cap.Platform != "" || cap.AccountType != "" || (cap.ID != PluginCapabilityIndependentTask && cap.ID != PluginCapabilityRequestHook) {
			return ErrPluginTaskCapability
		}
		taskCap = taskCap || cap.ID == PluginCapabilityIndependentTask
	}
	entry := false
	for _, v := range m.General.Tasks {
		entry = entry || v.Name == task.Entry
	}
	if !taskCap || !entry || m.ID == "" {
		return ErrPluginTaskCapability
	}
	seen := map[string]bool{}
	for _, cap := range m.General.HostCapabilities {
		if seen[cap] || !slices.Contains([]string{PluginHostKVV1, PluginHostNetworkReadV1, PluginHostPricingReadV1, PluginHostPricingWriteV1}, cap) {
			return ErrPluginTaskCapability
		}
		seen[cap] = true
	}
	scope := task.Scope
	if len(scope.PricingIDs) > pluginTaskMaxScopeItems || len(scope.ChannelIDs) > pluginTaskMaxScopeItems || len(scope.PricingFields) > 5 || len(scope.KVKeys) > pluginTaskMaxScopeItems || len(scope.NetworkURLs) > PluginTaskNetworkURLLimit {
		return ErrPluginTaskCapability
	}
	if len(scope.KVKeys) > 0 && !seen[PluginHostKVV1] || len(scope.NetworkURLs) > 0 && !seen[PluginHostNetworkReadV1] || (len(scope.ChannelIDs) > 0 || len(scope.PricingFields) > 0 || len(scope.PricingIDs) > 0) && (!seen[PluginHostPricingReadV1] && !seen[PluginHostPricingWriteV1]) {
		return ErrPluginTaskCapability
	}
	ids := map[int64]bool{}
	for _, id := range scope.ChannelIDs {
		if id <= 0 || ids[id] {
			return ErrPluginTaskCapability
		}
		ids[id] = true
	}
	for _, list := range [][]string{scope.PricingFields, scope.NetworkURLs, scope.KVKeys} {
		unique := map[string]bool{}
		for _, v := range list {
			if v == "" || unique[v] {
				return ErrPluginTaskCapability
			}
			unique[v] = true
		}
	}
	for _, f := range scope.PricingFields {
		if !validPluginPricingField(f) {
			return ErrPluginTaskCapability
		}
	}
	for _, k := range scope.KVKeys {
		if len(k) > 160 || strings.TrimSpace(k) != k || strings.ContainsAny(k, "\x00\r\n") {
			return ErrPluginTaskCapability
		}
	}

	if len(scope.PricingIDs) > 0 && len(scope.ChannelIDs) == 0 {
		return ErrPluginTaskCapability
	}
	pricingIDs := map[int64]bool{}
	for _, id := range scope.PricingIDs {
		if id <= 0 || pricingIDs[id] {
			return ErrPluginTaskCapability
		}
		pricingIDs[id] = true
	}
	for _, raw := range scope.NetworkURLs {
		if _, err := validatePluginTaskNetworkURL(raw); err != nil {
			return err
		}
	}
	return nil
}
