//go:build unit

package service

import (
	"context"
	"encoding/json"
	"errors"
	contract "github.com/Wei-Shaw/sub2api/pkg/pluginapi/contracts/v1"
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
	"time"
)

func capabilityFixture() (*PluginTaskClaim, *PluginInstanceSnapshot) {
	c := &PluginTaskClaim{Task: PluginTask{ID: 1, InstanceID: 7, Entry: "refresh", Input: json.RawMessage(`{"model":"x"}`), Generation: 1}, Run: PluginTaskRun{ID: 11, TaskID: 1, InstanceID: 7, IdempotencyKey: "job-1", ConfigRevision: 1, TaskGeneration: 1, FencingToken: 2}, InstanceGeneration: 1, LeaseUntil: time.Now().Add(time.Minute)}
	s := &PluginInstanceSnapshot{Instance: PluginInstance{ID: 7, Generation: 1, Enabled: true}, Revision: PluginInstanceRevision{InstanceID: 7, Revision: 1}, Installation: PluginInstallation{Manifest: PluginManifest{SchemaVersion: 2, ID: "sample.price", General: &PluginGeneralManifest{Tasks: []PluginTaskEntry{{Name: "refresh"}}}, Capabilities: []PluginCapability{{ID: PluginCapabilityIndependentTask}}}}}
	return c, s
}
func TestPluginTaskCapabilityPrepareEmptyAndNoSecrets(t *testing.T) {
	b := NewPluginTaskCapabilityBroker(nil, nil)
	c, s := capabilityFixture()
	s.Revision.ConfigEncrypted = "secret"
	s.Installation.ConfigEncrypted = "secret"
	s.Installation.BinaryPath = "private"
	raw, err := b.Prepare(context.Background(), c, s)
	require.NoError(t, err)
	require.JSONEq(t, `{"input":{"model":"x"},"host":{"kv":{},"network":[],"pricing":[]}}`, string(raw))
	require.NotContains(t, string(raw), "secret")
}
func TestPluginTaskCapabilityPrepareDenials(t *testing.T) {
	cases := []struct {
		name string
		edit func(*PluginTaskClaim, *PluginInstanceSnapshot)
	}{
		{"scope is not declaration", func(c *PluginTaskClaim, s *PluginInstanceSnapshot) { c.Task.Scope.KVKeys = []string{"x"} }},
		{"unsupported capability", func(c *PluginTaskClaim, s *PluginInstanceSnapshot) {
			s.Installation.Manifest.General.HostCapabilities = []string{"sql.any"}
		}},
		{"transport api", func(c *PluginTaskClaim, s *PluginInstanceSnapshot) { s.Installation.Manifest.Requires.TransportAPI = 1 }},
		{"transport capability", func(c *PluginTaskClaim, s *PluginInstanceSnapshot) {
			s.Installation.Manifest.Capabilities = append(s.Installation.Manifest.Capabilities, PluginCapability{ID: PluginCapabilityOpenAIOAuthOutbound})
		}},
		{"transport binding", func(c *PluginTaskClaim, s *PluginInstanceSnapshot) {
			s.Installation.Bindings = []PluginBinding{{Capability: PluginCapabilityOpenAIOAuthOutbound}}
		}},
		{"wrong instance", func(c *PluginTaskClaim, s *PluginInstanceSnapshot) { s.Instance.ID = 8 }},
		{"wrong entry", func(c *PluginTaskClaim, s *PluginInstanceSnapshot) { c.Task.Entry = "other" }},
		{"unknown price field", func(c *PluginTaskClaim, s *PluginInstanceSnapshot) {
			c.Task.Scope.PricingFields = []string{"image_output_price"}
		}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			c, s := capabilityFixture()
			tt.edit(c, s)
			_, err := NewPluginTaskCapabilityBroker(nil, nil).Prepare(context.Background(), c, s)
			require.Error(t, err)
		})
	}
}
func TestPluginTaskCapabilityManifestDoesNotGrantData(t *testing.T) {
	c, s := capabilityFixture()
	s.Installation.Manifest.General.HostCapabilities = []string{PluginHostKVV1, PluginHostNetworkReadV1, PluginHostPricingReadV1, PluginHostPricingWriteV1}
	raw, err := NewPluginTaskCapabilityBroker(nil, nil).Prepare(context.Background(), c, s)
	require.NoError(t, err)
	require.JSONEq(t, `{"input":{"model":"x"},"host":{"kv":{},"network":[],"pricing":[]}}`, string(raw))
}
func TestPluginTaskCapabilityOutputStrict(t *testing.T) {
	invalid := []string{`null`, `[]`, `{"other":1}`, `{"summary":"a","summary":"b"}`, `{"price_updates":[{"channel_id":1,"pricing_id":2,"fields":{"input_price":null}}]}`, `{"price_updates":[{"channel_id":1,"pricing_id":2,"fields":{"input_price":1e309}}]}`, `{"kv_updates":[{"key":"x","value":1,"delete":true}]}`, `{"summary":null}`, `{"price_updates":null}`}
	for _, raw := range invalid {
		t.Run(raw, func(t *testing.T) { _, err := decodePluginTaskOutput(json.RawMessage(raw)); require.Error(t, err) })
	}
	v, err := decodePluginTaskOutput(json.RawMessage(`{"summary":"ok","price_updates":[{"channel_id":1,"pricing_id":2,"fields":{"input_price":2.5}}],"kv_updates":[{"key":"x","value":null}]}`))
	require.NoError(t, err)
	require.Len(t, v.PriceUpdates, 1)
	require.Len(t, v.KVUpdates, 1)
}
func TestPluginTaskCapabilityCommitRequiresPreparedAndTransaction(t *testing.T) {
	b := NewPluginTaskCapabilityBroker(nil, nil)
	c, _ := capabilityFixture()
	err := b.Commit(context.Background(), nil, c, contract.TaskResult{Status: contract.TaskSucceeded, Output: json.RawMessage(`{}`)})
	require.True(t, errors.Is(err, ErrPluginTaskCapability))
}

func TestPluginTaskCapabilityDiscardAndFrozenAuthority(t *testing.T) {
	b := NewPluginTaskCapabilityBroker(nil, nil)
	c, s := capabilityFixture()
	_, err := b.Prepare(context.Background(), c, s)
	require.NoError(t, err)
	require.Len(t, b.grants, 1)
	b.Discard(c)
	require.Empty(t, b.grants)
	b.Discard(nil)
	c.Run.ConfigRevision++
	_, err = b.Prepare(context.Background(), c, s)
	require.Error(t, err)
	c.Run.ConfigRevision--
	c.InstanceGeneration++
	_, err = b.Prepare(context.Background(), c, s)
	require.Error(t, err)
}
func TestPluginTaskCapabilityEnvelope64KiB(t *testing.T) {
	c, s := capabilityFixture()
	c.Task.Input = json.RawMessage(`{"blob":"` + strings.Repeat("x", PluginTaskCapabilityEnvelopeLimit-12) + `"}`)
	require.LessOrEqual(t, len(c.Task.Input), PluginTaskCapabilityEnvelopeLimit)
	_, err := NewPluginTaskCapabilityBroker(nil, nil).Prepare(context.Background(), c, s)
	require.Error(t, err)
	_, err = decodePluginTaskOutput(json.RawMessage(`{"summary":"` + strings.Repeat("x", PluginTaskCapabilityOutputLimit) + `"}`))
	require.Error(t, err)
}

func TestPluginTaskCapabilityInvalidPrepareRevokesGrant(t *testing.T) {
	b := NewPluginTaskCapabilityBroker(nil, nil)
	c, s := capabilityFixture()
	_, err := b.Prepare(context.Background(), c, s)
	require.NoError(t, err)
	s.Installation.Manifest.Requires.TransportAPI = 1
	_, err = b.Prepare(context.Background(), c, s)
	require.Error(t, err)
	require.Empty(t, b.grants)
}
func TestPluginTaskCapabilityCancelledPrepareNoGrant(t *testing.T) {
	b := NewPluginTaskCapabilityBroker(nil, nil)
	c, s := capabilityFixture()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := b.Prepare(ctx, c, s)
	require.Error(t, err)
	require.Empty(t, b.grants)
}

func TestPluginTaskCapabilityCRUDStaticValidation(t *testing.T) {
	c, s := capabilityFixture()
	c.Task.ID = 0
	c.Run = PluginTaskRun{} // CRUD needs no persisted task/run.
	require.NoError(t, ValidatePluginTaskCapabilities(c.Task, s.Installation.Manifest))
	for _, tt := range []struct {
		name  string
		scope PluginTaskScope
		caps  []string
	}{
		{"undeclared kv", PluginTaskScope{KVKeys: []string{"x"}}, nil},
		{"undeclared pricing", PluginTaskScope{ChannelIDs: []int64{1}, PricingFields: []string{"input_price"}}, nil},
		{"unknown field", PluginTaskScope{ChannelIDs: []int64{1}, PricingFields: []string{"image_output_price"}}, []string{PluginHostPricingReadV1}},
		{"HTTP", PluginTaskScope{NetworkURLs: []string{"http://example.com"}}, []string{PluginHostNetworkReadV1}},
		{"userinfo", PluginTaskScope{NetworkURLs: []string{"https://user:password@example.com"}}, []string{PluginHostNetworkReadV1}},
		{"private literal", PluginTaskScope{NetworkURLs: []string{"https://10.0.0.1"}}, []string{PluginHostNetworkReadV1}},
		{"loopback", PluginTaskScope{NetworkURLs: []string{"https://localhost"}}, []string{PluginHostNetworkReadV1}},
		{"negative pricing id", PluginTaskScope{ChannelIDs: []int64{1}, PricingIDs: []int64{-1}}, []string{PluginHostPricingWriteV1}},
		{"duplicate pricing id", PluginTaskScope{ChannelIDs: []int64{1}, PricingIDs: []int64{101, 101}}, []string{PluginHostPricingReadV1}},
		{"entry without channel", PluginTaskScope{PricingIDs: []int64{101}}, []string{PluginHostPricingReadV1}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			task := c.Task
			task.Scope = tt.scope
			m := s.Installation.Manifest
			g := *m.General
			g.HostCapabilities = tt.caps
			m.General = &g
			require.Error(t, ValidatePluginTaskCapabilities(task, m))
		})
	}
	m := s.Installation.Manifest
	g := *m.General
	g.HostCapabilities = []string{PluginHostPricingReadV1, PluginHostNetworkReadV1, PluginHostKVV1}
	m.General = &g
	c.Task.Scope = PluginTaskScope{ChannelIDs: []int64{1}, PricingIDs: []int64{101}, PricingFields: []string{"input_price"}, NetworkURLs: []string{"https://example.com/prices"}, KVKeys: []string{"cursor"}}
	require.NoError(t, ValidatePluginTaskCapabilities(c.Task, m))
	c.Task.Scope.PricingIDs = make([]int64, 129)
	for i := range c.Task.Scope.PricingIDs {
		c.Task.Scope.PricingIDs[i] = int64(i + 1)
	}
	require.Error(t, ValidatePluginTaskCapabilities(c.Task, m))
}
