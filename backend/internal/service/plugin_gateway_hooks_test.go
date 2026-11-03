package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Wei-Shaw/sub2api/internal/pluginflow"
	contract "github.com/Wei-Shaw/sub2api/pkg/pluginapi/contracts/v1"
	"github.com/stretchr/testify/require"
	"sync"
	"testing"
)

type pghLease struct {
	snapshot        PluginInstanceSnapshot
	result          contract.HookResult
	err             error
	calls, released int
	content         []byte
}

func (l *pghLease) Snapshot() (*PluginInstanceSnapshot, error) { return &l.snapshot, nil }
func (l *pghLease) Check(_ context.Context, _ int64, r contract.HookRequest) (contract.HookResult, error) {
	l.calls++
	l.content = append([]byte(nil), r.Content.Data...)
	return l.result, l.err
}
func (l *pghLease) Release() { l.released++ }

type pghSource struct {
	mu        sync.Mutex
	leases    map[contract.Stage][]PluginGatewayLease
	failStage contract.Stage
	calls     []contract.Stage
}

func (s *pghSource) AcquireGroup(_ context.Context, _ int64, stage contract.Stage, _ string) ([]PluginGatewayLease, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, stage)
	if stage == s.failStage {
		return nil, errors.New("PRIVATE configuration path")
	}
	return s.leases[stage], nil
}
func pghMakeLease(stage contract.Stage, id int64, result contract.HookResult) *pghLease {
	return &pghLease{snapshot: PluginInstanceSnapshot{Instance: PluginInstance{ID: id, Enabled: true, CurrentRevision: 1}, Revision: PluginInstanceRevision{Revision: 1}, Installation: PluginInstallation{Version: "1.0.0"}, Bindings: []PluginGroupBinding{{ID: id, InstanceID: id, GroupID: 42, Stage: stage, Entry: "inspect", Protocol: "openai.chat_completions.v1", Enabled: true, CanReplace: true}}}, result: result}
}
func TestPluginGatewayHooksInputBlocks(t *testing.T) {
	input := pghMakeLease(contract.Input, 1, contract.HookResult{Decision: contract.Block, Rejection: &contract.Rejection{Code: "denied", Message: "PRIVATE BODY"}})
	source := &pghSource{leases: map[contract.Stage][]PluginGatewayLease{contract.Input: {input}}}
	svc := NewPluginGatewayServiceWithSource(source)
	p, err := svc.Prepare(context.Background(), 42, "openai.chat_completions.v1")
	require.NoError(t, err)
	defer p.Close()
	require.True(t, p.Bound())
	body, err := p.CheckInput(context.Background(), "request-1", []byte(`{"model":"m","messages":[{"role":"user","content":"PRIVATE"}]}`))
	require.ErrorIs(t, err, ErrPluginGatewayBlocked)
	require.Nil(t, body)
	require.NotContains(t, err.Error(), "PRIVATE")
	require.Equal(t, 1, input.calls)
}
func TestPluginGatewayHooksOutputPinnedBeforeForward(t *testing.T) {
	input := pghMakeLease(contract.Input, 1, contract.HookResult{Decision: contract.Pass})
	output := pghMakeLease(contract.Output, 2, contract.HookResult{Decision: contract.Pass})
	source := &pghSource{leases: map[contract.Stage][]PluginGatewayLease{contract.Input: {input}, contract.Output: {output}}}
	svc := NewPluginGatewayServiceWithSource(source)
	p, err := svc.Prepare(context.Background(), 42, "openai.chat_completions.v1")
	require.NoError(t, err)
	require.True(t, p.HasOutput())
	require.Equal(t, []contract.Stage{contract.Input, contract.Output}, source.calls)
	body := []byte(`{"model":"m","messages":[{"role":"user","content":"hello"}]}`)
	got, err := p.CheckInput(context.Background(), "r", body)
	require.NoError(t, err)
	require.Equal(t, body, got)
	c, err := p.CheckOutput(context.Background(), "r", contract.Content{MediaType: "text/plain", Data: []byte("whole response")})
	require.NoError(t, err)
	require.Equal(t, "whole response", string(c.Data))
	require.Equal(t, 1, output.calls)
	p.Close()
	p.Close()
	require.Equal(t, 1, input.released)
	require.Equal(t, 1, output.released)
}
func TestPluginGatewayHooksPrepareFailureReleasesInput(t *testing.T) {
	input := pghMakeLease(contract.Input, 1, contract.HookResult{Decision: contract.Pass})
	source := &pghSource{leases: map[contract.Stage][]PluginGatewayLease{contract.Input: {input}}, failStage: contract.Output}
	p, err := NewPluginGatewayServiceWithSource(source).Prepare(context.Background(), 42, "openai.chat_completions.v1")
	require.ErrorIs(t, err, ErrPluginGatewayCheck)
	require.Nil(t, p)
	require.Equal(t, 1, input.released)
	require.NotContains(t, err.Error(), "PRIVATE")
}
func TestPluginGatewayHooksUnboundDoesNotInvoke(t *testing.T) {
	source := &pghSource{leases: map[contract.Stage][]PluginGatewayLease{}}
	p, err := NewPluginGatewayServiceWithSource(source).Prepare(context.Background(), 42, "unsupported.media")
	require.NoError(t, err)
	require.False(t, p.Bound())
	p.Close()
}

func TestPluginGatewayHooksCacheBypass(t *testing.T) {
	s, _, store := rcRequestPolicyService(t)
	ctx := rcBind(t, s, context.Background(), rcRequestIntent("read_write"))
	ctx = WithPluginGatewayCacheBypass(ctx)
	q, err := s.Begin(ctx, rcRequestIntent("read_write"), rcTestRequest(), ResultCacheAccess{})
	require.NoError(t, err)
	require.Equal(t, "BYPASS", q.Status)
	require.Equal(t, "plugin_bound", q.Reason)
	require.Empty(t, q.Key)
	require.Empty(t, q.token)
	q.Complete(ctx, 200, rcBody)
	store.mu.Lock()
	require.Nil(t, store.entry)
	store.mu.Unlock()
}
func TestPluginGatewayHooksCompatibility(t *testing.T) {
	m := testPluginGeneralManifest()
	m.General.Tasks = nil
	m.General.HostCapabilities = nil
	m.Capabilities = []PluginCapability{{ID: PluginCapabilityRequestHook}}
	m.Requires.Sub2API = ">=0.1.0 <1.0.0"
	result := EvaluatePluginCompatibility(m, PluginHostInfo{Version: "0.1.179"})
	require.True(t, result.Compatible)
	require.NotEqual(t, "general_runtime_unavailable", result.ReasonCode)
	for _, kind := range []string{"task", "host_capability", "unknown_protocol"} {
		t.Run(kind, func(t *testing.T) {
			bad := testPluginGeneralManifest()
			bad.General.Tasks = nil
			bad.General.HostCapabilities = nil
			bad.Capabilities = []PluginCapability{{ID: PluginCapabilityRequestHook}}
			bad.Requires.Sub2API = m.Requires.Sub2API
			switch kind {
			case "task":
				bad.General.Tasks = []PluginTaskEntry{{Name: "sync"}}
				bad.Capabilities = append(bad.Capabilities, PluginCapability{ID: PluginCapabilityIndependentTask})
			case "host_capability":
				bad.General.HostCapabilities = []string{"storage.kv.read"}
			case "unknown_protocol":
				bad.General.Hooks[0].Protocols = []string{"unknown.v1"}
			}
			result := EvaluatePluginCompatibility(bad, PluginHostInfo{Version: "0.1.179"})
			if kind == "task" {
				// M5 opens only implemented independent-task declarations.
				require.True(t, result.Compatible)
				require.NotEqual(t, "general_runtime_unavailable", result.ReasonCode)
				return
			}
			require.False(t, result.Compatible)
			require.Equal(t, "general_runtime_unavailable", result.ReasonCode)
		})
	}
}

func TestPluginGatewayHooksIntermediateReplacementControls(t *testing.T) {
	first := pghMakeLease(contract.Input, 1, contract.HookResult{Decision: contract.Replace, Content: &contract.Content{MediaType: "application/json", Data: []byte(`{"model":"different","messages":[{"role":"user","content":"hello"}]}`)}})
	second := pghMakeLease(contract.Input, 2, contract.HookResult{Decision: contract.Replace, Content: &contract.Content{MediaType: "application/json", Data: []byte(`{"model":"m","messages":[{"role":"user","content":"hello"}]}`)}})
	p, err := NewPluginGatewayServiceWithSource(&pghSource{leases: map[contract.Stage][]PluginGatewayLease{contract.Input: {first, second}}}).Prepare(context.Background(), 42, PluginProtocolOpenAIChat)
	require.NoError(t, err)
	defer p.Close()
	_, err = p.CheckInput(context.Background(), "r", []byte(`{"model":"m","messages":[{"role":"user","content":"hello"}]}`))
	require.ErrorIs(t, err, ErrPluginGatewayCheck)
	require.Zero(t, second.calls, "invalid intermediate replacement must not reach another plugin")
}

type pghRealRuntime struct{ piFakeRuntime }

func (r *pghRealRuntime) HookInvoker(_, _ string) pluginflow.Invoker {
	return func(_ context.Context, _ pluginflow.Binding, req contract.HookRequest) (contract.HookResult, error) {
		data := []byte("output:" + r.marker)
		if req.Stage == contract.Input {
			data = []byte(fmt.Sprintf(`{"model":"m","messages":[{"role":"user","content":%q}]}`, r.marker))
		}
		return contract.HookResult{Decision: contract.Replace, Content: &contract.Content{MediaType: req.Content.MediaType, Data: data}}, nil
	}
}

// Real publication, AcquireGroup and PluginInstanceLease.Check; only persistence
// and the subprocess transport are deterministic fixtures.
func TestPluginGatewayHooksRealInstanceLeasePublication(t *testing.T) {
	repo := newPIMemoryRepo()
	repo.s.Bindings = append(repo.s.Bindings, PluginGroupBinding{ID: 2, InstanceID: 11, GroupID: 7, Stage: contract.Output, Entry: "inspect", Protocol: PluginProtocolOpenAIChat, Enabled: true, CanReplace: true})
	started := []*pghRealRuntime{}
	instances := newPluginInstanceService(repo, piTestCipher{}, func(_ context.Context, _ *PluginInstanceSnapshot, config json.RawMessage) (pluginInstanceRuntime, error) {
		var c struct{ Marker string }
		if err := json.Unmarshal(config, &c); err != nil {
			return nil, err
		}
		r := &pghRealRuntime{piFakeRuntime: piFakeRuntime{marker: c.Marker}}
		started = append(started, r)
		return r, nil
	})
	defer instances.Close()
	gateway := NewPluginGatewayService(instances)
	old, err := gateway.Prepare(context.Background(), 7, PluginProtocolOpenAIChat)
	require.NoError(t, err)
	defer old.Close()
	require.True(t, old.HasOutput())
	draft, err := instances.SaveConfig(context.Background(), 11, 1, json.RawMessage(`{"marker":"beta"}`))
	require.NoError(t, err)
	_, err = instances.Publish(context.Background(), 11, draft.Instance.Generation, draft.Revision.Revision)
	require.NoError(t, err)
	fresh, err := gateway.Prepare(context.Background(), 7, PluginProtocolOpenAIChat)
	require.NoError(t, err)
	defer fresh.Close()
	body := []byte(`{"model":"m","messages":[{"role":"user","content":"hello"}]}`)
	for _, item := range []struct {
		plan   *PluginGatewayRequest
		marker string
	}{{old, "alpha"}, {fresh, "beta"}} {
		input, err := item.plan.CheckInput(context.Background(), "r", body)
		require.NoError(t, err)
		require.Contains(t, string(input), item.marker)
		out, err := item.plan.CheckOutput(context.Background(), "r", contract.Content{MediaType: "text/plain", Data: []byte("whole")})
		require.NoError(t, err)
		require.Equal(t, "output:"+item.marker, string(out.Data))
	}
	require.Zero(t, started[0].closed.Load())
	old.Close()
	require.EqualValues(t, 1, started[0].closed.Load())
	_, err = old.CheckOutput(context.Background(), "r", contract.Content{MediaType: "text/plain", Data: []byte("whole")})
	require.ErrorIs(t, err, ErrPluginGatewayCheck)
}

func TestPluginGatewayHooksOutputReplacementMediaType(t *testing.T) {
	for _, media := range []string{"text/plain; charset=utf-8", "text/plain; charset=iso-8859-1"} {
		t.Run(media, func(t *testing.T) {
			lease := pghMakeLease(contract.Output, 1, contract.HookResult{Decision: contract.Replace, Content: &contract.Content{MediaType: media, Data: []byte("approved")}})
			plan, e := NewPluginGatewayServiceWithSource(&pghSource{leases: map[contract.Stage][]PluginGatewayLease{contract.Output: {lease}}}).Prepare(context.Background(), 42, PluginProtocolOpenAIChat)
			require.NoError(t, e)
			defer plan.Close()
			_, e = plan.CheckOutput(context.Background(), "r", contract.Content{MediaType: "text/plain; charset=utf-8", Data: []byte("original")})
			if media == "text/plain; charset=utf-8" {
				require.NoError(t, e)
			} else {
				require.ErrorIs(t, e, ErrPluginGatewayCheck)
			}
		})
	}
}

func TestPluginGatewayHooksSchedulingGroupDrift(t *testing.T) {
	a, b, c := int64(42), int64(43), int64(44)
	repo := &pghGroupRepository{groups: map[int64]*Group{a: {ID: a, ClaudeCodeOnly: true, FallbackGroupID: &b}, b: {ID: b}, c: {ID: c}}}
	gateway := &GatewayService{groupRepo: repo}
	ctx := WithPluginGatewayEffectiveGroup(context.Background(), b)
	_, gid, err := gateway.resolveGatewayGroup(ctx, &a)
	require.NoError(t, err)
	require.Equal(t, b, *gid)
	repo.groups[a].FallbackGroupID = &c
	_, _, err = gateway.resolveGatewayGroup(ctx, &a)
	require.ErrorIs(t, err, ErrPluginGatewayCheck)
}

// This untagged fixture is independent of unit-only gateway mocks.
type pghGroupRepository struct {
	GroupRepository
	groups map[int64]*Group
}

func (r *pghGroupRepository) GetByID(_ context.Context, id int64) (*Group, error) {
	if group, ok := r.groups[id]; ok {
		return group, nil
	}
	return nil, ErrGroupNotFound
}
func (r *pghGroupRepository) GetByIDLite(ctx context.Context, id int64) (*Group, error) {
	return r.GetByID(ctx, id)
}
