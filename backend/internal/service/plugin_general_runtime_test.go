package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pluginflow"
	contract "github.com/Wei-Shaw/sub2api/pkg/pluginapi/contracts/v1"
	general "github.com/Wei-Shaw/sub2api/pkg/pluginapi/general/v1"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

func runtimeTestInfo(m PluginManifest) *general.InfoResponse {
	g := m.General
	info := &general.InfoResponse{PluginId: m.ID, PluginVersion: m.Version, ProtocolVersion: 1, GeneralApiVersion: 1, ConfigVersion: int64(g.ConfigVersion), Capabilities: []string{PluginCapabilityRequestHook, PluginCapabilityIndependentTask}, HostCapabilities: append([]string(nil), g.HostCapabilities...)}
	for _, h := range g.Hooks {
		stages := []string{}
		for _, s := range h.Stages {
			stages = append(stages, string(s))
		}
		info.Hooks = append(info.Hooks, &general.HookDeclaration{Name: h.Name, Stages: stages, Protocols: append([]string(nil), h.Protocols...), CanReplace: h.CanReplace})
	}
	for _, task := range g.Tasks {
		info.Tasks = append(info.Tasks, &general.TaskDeclaration{Name: task.Name})
	}
	return info
}

func TestPluginGeneralRuntimeHandshakeMatchesManifest(t *testing.T) {
	m := testPluginGeneralManifest()
	info := runtimeTestInfo(m)
	require.NoError(t, validateGeneralRuntimeInfo(m, info))
	for _, tt := range []struct {
		name string
		edit func(*general.InfoResponse)
	}{
		{"id", func(i *general.InfoResponse) { i.PluginId = "com.example.wrong" }},
		{"version", func(i *general.InfoResponse) { i.PluginVersion = "9.0.0" }},
		{"protocol", func(i *general.InfoResponse) { i.ProtocolVersion = 2 }},
		{"api", func(i *general.InfoResponse) { i.GeneralApiVersion = 2 }},
		{"config", func(i *general.InfoResponse) { i.ConfigVersion++ }},
		{"missing hook", func(i *general.InfoResponse) { i.Hooks = nil }},
		{"nil hook", func(i *general.InfoResponse) { i.Hooks[0] = nil }},
		{"extra hook", func(i *general.InfoResponse) { i.Hooks = append(i.Hooks, i.Hooks[0]) }},
		{"stage", func(i *general.InfoResponse) { i.Hooks[0].Stages = []string{"task"} }},
		{"duplicate stage", func(i *general.InfoResponse) { i.Hooks[0].Stages = []string{"input", "input"} }},
		{"protocols", func(i *general.InfoResponse) { i.Hooks[0].Protocols = []string{"other.protocol.v1"} }},
		{"replace", func(i *general.InfoResponse) { i.Hooks[0].CanReplace = false }},
		{"missing task", func(i *general.InfoResponse) { i.Tasks = nil }},
		{"nil task", func(i *general.InfoResponse) { i.Tasks[0] = nil }},
		{"extra task", func(i *general.InfoResponse) { i.Tasks = append(i.Tasks, i.Tasks[0]) }},
		{"capability", func(i *general.InfoResponse) { i.Capabilities[0] = "transport.v1" }},
		{"host grant", func(i *general.InfoResponse) {
			i.HostCapabilities = append(i.HostCapabilities, "accounts.credentials.read")
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			copy := proto.Clone(info).(*general.InfoResponse)
			tt.edit(copy)
			require.ErrorIs(t, validateGeneralRuntimeInfo(m, copy), ErrGeneralHandshake)
		})
	}
	require.ErrorIs(t, validateGeneralRuntimeInfo(m, nil), ErrGeneralHandshake)
	info.Hooks[0].Stages = []string{"output", "input"}
	info.Capabilities[0], info.Capabilities[1] = info.Capabilities[1], info.Capabilities[0]
	require.NoError(t, validateGeneralRuntimeInfo(m, info))
}

func TestPluginGeneralWireResults(t *testing.T) {
	score := &general.Finding_Score{Score: 0.99}
	valid := &general.HookResponse{Decision: "block", Rejection: &general.Rejection{Code: "policy", Message: "Blocked"}, Findings: []*general.Finding{{RuleId: "r1", RuleVersion: "v1", Confidence: score}}}
	result, err := decodeGeneralHookResult(valid, false)
	require.NoError(t, err)
	require.Equal(t, contract.Block, result.Decision)
	require.Equal(t, 0.99, *result.Findings[0].Confidence)
	valid.Findings[0].GetConfidence().(*general.Finding_Score).Score = 0.1
	require.Equal(t, 0.99, *result.Findings[0].Confidence)
	for _, r := range []*general.HookResponse{nil, {Decision: "weird"}, {Decision: "pass", Findings: []*general.Finding{nil}}, {Decision: "replace", Content: &general.Content{MediaType: "text/plain", Data: []byte("x")}}, {Decision: "block", Content: &general.Content{}, Rejection: &general.Rejection{Code: "policy", Message: "secret"}}} {
		result, err := decodeGeneralHookResult(r, false)
		require.Error(t, err)
		require.Equal(t, contract.HookResult{}, result)
	}
	_, err = decodeGeneralHookResult(&general.HookResponse{Decision: "replace", Content: &general.Content{MediaType: "text/plain", Data: make([]byte, generalBodyMaxBytes+1)}}, true)
	require.Error(t, err)
	task, err := decodeGeneralTaskResult(&general.TaskResponse{Status: "failed", Changed: true, ErrorCode: "partial_failure", OutputJson: []byte(`{"ok":false}`)})
	require.NoError(t, err)
	require.True(t, task.Changed)
	for _, r := range []*general.TaskResponse{nil, {Status: "pass"}, {Status: "succeeded", OutputJson: []byte(`{`)}, {Status: "succeeded", OutputJson: make([]byte, generalJSONMaxBytes+1)}} {
		got, err := decodeGeneralTaskResult(r)
		require.Error(t, err)
		require.Equal(t, contract.TaskResult{}, got)
	}
}

func TestPluginGeneralRuntimeProcess(t *testing.T) {
	if testing.Short() {
		t.Skip("real subprocess test")
	}
	root := t.TempDir()
	binary := filepath.Join(root, "general-fixture.exe")
	cmd := exec.Command("go", "build", "-o", binary, "./testdata/general-plugin")
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, string(output))
	data, err := os.ReadFile(binary)
	require.NoError(t, err)
	digest := sha256.Sum256(data)
	installation := &PluginInstallation{Manifest: testPluginGeneralManifest(), BinaryPath: binary, BinarySHA256: hex.EncodeToString(digest[:])}
	installation.PluginKey = installation.Manifest.ID
	installation.Version = installation.Manifest.Version
	installation.Manifest.General.Hooks[0].Protocols = []string{"example.text.v1"}
	installation.Manifest.General.HostCapabilities = nil
	installation.Manifest.Files["bin/plugin"] = installation.BinarySHA256
	options := func(id int64, config string) generalInstanceOptions {
		return generalInstanceOptions{InstanceID: id, ConfigRevision: 1, Config: json.RawMessage(config), Host: PluginHostInfo{Version: "0.1.179"}, StartTimeout: 10 * time.Second, CallTimeout: 250 * time.Millisecond, SocketDir: root}
	}
	start := func(t *testing.T, id int64, config string) *generalPluginRuntime {
		t.Helper()
		r, err := startGeneralPluginRuntime(context.Background(), installation, options(id, config))
		require.NoError(t, err)
		t.Cleanup(r.Close)
		return r
	}
	request := contract.HookRequest{RequestID: "req", AttemptID: "attempt", GroupID: 7, Stage: contract.Input, Content: contract.Content{MediaType: "text/plain", Data: []byte("replace")}}
	binding := func(id int64) pluginflow.Binding {
		return pluginflow.Binding{ID: 1, InstanceID: id, GroupID: 7, Stage: contract.Input, Enabled: true, Ready: true, Version: installation.Version, ConfigRevision: 1, CanReplace: true}
	}
	t.Run("isolated immutable instances and chain", func(t *testing.T) {
		a := start(t, 11, `{"marker":"alpha"}`)
		b := start(t, 12, `{"marker":"beta"}`)
		out, trace, err := pluginflow.RunWithTrace(context.Background(), request, []pluginflow.Binding{binding(11)}, a.HookInvoker("inspect", "example.text.v1"))
		require.NoError(t, err)
		require.Equal(t, "alpha", string(out.Content.Data))
		require.Len(t, trace.Records, 1)
		rb, err := b.HookInvoker("inspect", "example.text.v1")(context.Background(), binding(12), request)
		require.NoError(t, err)
		require.Equal(t, "beta", string(rb.Content.Data))
		result, err := a.RunTask(context.Background(), "sync_prices", "job-1", json.RawMessage(`{}`))
		require.NoError(t, err)
		require.JSONEq(t, `{"marker":"alpha"}`, string(result.Output))
		input := request
		input.Content.Data = []byte("block")
		out, _, err = pluginflow.RunWithTrace(context.Background(), input, []pluginflow.Binding{binding(11)}, a.HookInvoker("inspect", "example.text.v1"))
		require.NoError(t, err)
		require.Equal(t, contract.Block, out.Decision)
		require.Nil(t, out.Content)
		input.Content.Data = []byte("pass")
		pass, err := a.HookInvoker("inspect", "example.text.v1")(context.Background(), binding(11), input)
		require.NoError(t, err)
		require.Equal(t, contract.Pass, pass.Decision)
		denied := binding(11)
		denied.CanReplace = false
		got, err := a.HookInvoker("inspect", "example.text.v1")(context.Background(), denied, request)
		require.Error(t, err)
		require.Equal(t, contract.HookResult{}, got)
		for _, inputText := range []string{"invalid", "oversize"} {
			input.Content.Data = []byte(inputText)
			got, err := a.HookInvoker("inspect", "example.text.v1")(context.Background(), binding(11), input)
			require.Error(t, err)
			require.Equal(t, contract.HookResult{}, got)
			require.NotContains(t, err.Error(), "secret")
		}
		require.NoError(t, a.life.Err())
		input.Content.Data = []byte("error")
		_, err = a.HookInvoker("inspect", "example.text.v1")(context.Background(), binding(11), input)
		require.ErrorIs(t, err, ErrGeneralInvocation)
		require.NotContains(t, err.Error(), "secret")
		require.True(t, a.client.Exited())
	})
	t.Run("binding and declaration mismatch never invokes", func(t *testing.T) {
		r := start(t, 21, `{}`)
		for _, edit := range []func(*pluginflow.Binding){func(b *pluginflow.Binding) { b.InstanceID++ }, func(b *pluginflow.Binding) { b.ConfigRevision++ }, func(b *pluginflow.Binding) { b.Version = "9.0.0" }, func(b *pluginflow.Binding) { b.GroupID++ }, func(b *pluginflow.Binding) { b.Stage = contract.Output }, func(b *pluginflow.Binding) { b.Enabled = false }, func(b *pluginflow.Binding) { b.Ready = false }} {
			b := binding(21)
			edit(&b)
			got, err := r.HookInvoker("inspect", "example.text.v1")(context.Background(), b, request)
			require.ErrorIs(t, err, ErrGeneralBinding)
			require.Equal(t, contract.HookResult{}, got)
		}
		_, err := r.HookInvoker("missing", "example.text.v1")(context.Background(), binding(21), request)
		require.ErrorIs(t, err, ErrGeneralBinding)
		_, err = r.HookInvoker("inspect", "wrong.protocol.v1")(context.Background(), binding(21), request)
		require.ErrorIs(t, err, ErrGeneralBinding)
		_, err = r.RunTask(context.Background(), "missing", "job", json.RawMessage(`{}`))
		require.ErrorIs(t, err, ErrGeneralBinding)
		status, err := r.RunTask(context.Background(), "sync_prices", "count", json.RawMessage(`{}`))
		require.NoError(t, err)
		require.JSONEq(t, `{"calls":0}`, string(status.Output))
	})
	t.Run("deadlines cancellation close", func(t *testing.T) {
		r := start(t, 31, `{}`)
		input := request
		input.Content.Data = []byte("wait")
		_, err := r.HookInvoker("inspect", "example.text.v1")(context.Background(), binding(31), input)
		require.ErrorIs(t, err, context.DeadlineExceeded)
		require.True(t, r.client.Exited())
		r = start(t, 31, `{}`)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err = r.RunTask(ctx, "sync_prices", "job", json.RawMessage(`{}`))
		require.ErrorIs(t, err, context.Canceled)
		result, err := r.RunTask(context.Background(), "sync_prices", "partial", json.RawMessage(`{}`))
		require.NoError(t, err)
		require.Equal(t, contract.TaskFailed, result.Status)
		require.True(t, result.Changed)
		r.Close()
		r.Close()
		require.True(t, r.client.Exited())
		_, err = r.RunTask(context.Background(), "sync_prices", "job", json.RawMessage(`{}`))
		require.ErrorIs(t, err, ErrGeneralClosed)
	})
	t.Run("noncooperative deadline destroys uncertain instance", func(t *testing.T) {
		r := start(t, 51, `{}`)
		input := request
		input.Content.Data = []byte("ignore-cancel")
		got, err := r.HookInvoker("inspect", "example.text.v1")(context.Background(), binding(51), input)
		require.ErrorIs(t, err, context.DeadlineExceeded)
		require.Equal(t, contract.HookResult{}, got)
		require.True(t, r.client.Exited(), "deadline must terminate noncooperative process")
		_, err = r.RunTask(context.Background(), "sync_prices", "job", json.RawMessage(`{}`))
		require.ErrorIs(t, err, ErrGeneralClosed)
	})
	t.Run("creation context and declarations are snapshots", func(t *testing.T) {
		opt := options(61, `{"marker":"stable"}`)
		ctx, cancel := context.WithCancel(context.Background())
		r, err := startGeneralPluginRuntime(ctx, installation, opt)
		require.NoError(t, err)
		t.Cleanup(r.Close)
		cancel()
		opt.Config[0] = '['
		original := installation.Manifest.General.Hooks[0].Protocols[0]
		installation.Manifest.General.Hooks[0].Protocols[0] = "changed.protocol.v1"
		defer func() { installation.Manifest.General.Hooks[0].Protocols[0] = original }()
		result, err := r.HookInvoker("inspect", "example.text.v1")(context.Background(), binding(61), request)
		require.NoError(t, err)
		require.Equal(t, "stable", string(result.Content.Data))
		_, err = r.api.Configure(context.Background(), &general.ConfigureRequest{InstanceId: 61, ConfigRevision: 2, ConfigJson: []byte(`{}`)})
		require.Error(t, err, "fixture must reject in-place configuration changes")
	})
	t.Run("close cancels active and queued calls", func(t *testing.T) {
		opt := options(71, `{}`)
		opt.CallTimeout = 5 * time.Second
		r, err := startGeneralPluginRuntime(context.Background(), installation, opt)
		require.NoError(t, err)
		t.Cleanup(r.Close)
		input := request
		input.Content.Data = []byte("ignore-cancel")
		active := make(chan error, 1)
		go func() {
			_, e := r.HookInvoker("inspect", "example.text.v1")(context.Background(), binding(71), input)
			active <- e
		}()
		require.Eventually(t, func() bool { return len(r.slot) == 1 }, time.Second, time.Millisecond)
		queued := make(chan error, 1)
		go func() {
			_, e := r.RunTask(context.Background(), "sync_prices", "job", json.RawMessage(`{}`))
			queued <- e
		}()
		r.Close()
		for _, ch := range []chan error{active, queued} {
			select {
			case e := <-ch:
				require.Error(t, e)
			case <-time.After(2 * time.Second):
				t.Fatal("operation outlived closed process")
			}
		}
		require.True(t, r.client.Exited())
	})
	t.Run("wire limit retires process", func(t *testing.T) {
		r := start(t, 81, `{}`)
		input := request
		input.Content.Data = []byte("wire-oversize")
		result, err := r.HookInvoker("inspect", "example.text.v1")(context.Background(), binding(81), input)
		require.Error(t, err)
		require.Equal(t, contract.HookResult{}, result)
		require.True(t, r.client.Exited())
	})
	t.Run("startup deadline", func(t *testing.T) {
		stalled := *installation
		marker := filepath.Join(root, "stall-info.marker")
		require.NoError(t, os.WriteFile(marker, nil, 0600))
		t.Cleanup(func() { _ = os.Remove(marker) })
		opt := options(91, `{}`)
		opt.StartTimeout = 250 * time.Millisecond
		started := time.Now()
		r, err := startGeneralPluginRuntime(context.Background(), &stalled, opt)
		require.Nil(t, r)
		require.ErrorIs(t, err, context.DeadlineExceeded)
		require.Less(t, time.Since(started), 3*time.Second)
	})
	t.Run("configuration and handshake rejection", func(t *testing.T) {
		for _, raw := range []string{`null`, `[]`, `{`, strings.Repeat(" ", generalJSONMaxBytes+1)} {
			_, err := startGeneralPluginRuntime(context.Background(), installation, options(41, raw))
			require.ErrorIs(t, err, ErrGeneralConfig)
		}
		for _, raw := range []string{`{"reject":true}`, `{"wrong_echo":true}`} {
			r, err := startGeneralPluginRuntime(context.Background(), installation, options(41, raw))
			require.Error(t, err)
			require.Nil(t, r)
		}
		bad := *installation
		bad.Manifest = testPluginManifest(nil)
		_, err := startGeneralPluginRuntime(context.Background(), &bad, options(41, `{}`))
		require.Error(t, err)
		bad = *installation
		bad.Manifest.ID = "com.example.wrong"
		bad.PluginKey = bad.Manifest.ID
		r, err := startGeneralPluginRuntime(context.Background(), &bad, options(41, `{}`))
		require.ErrorIs(t, err, ErrGeneralHandshake)
		require.Nil(t, r)
		bad = *installation
		bad.BinarySHA256 = strings.Repeat("0", 64)
		bad.Manifest.Files = make(map[string]string)
		for name, value := range installation.Manifest.Files {
			bad.Manifest.Files[name] = value
		}
		bad.Manifest.Files["bin/plugin"] = bad.BinarySHA256
		r, err = startGeneralPluginRuntime(context.Background(), &bad, options(41, `{}`))
		require.Error(t, err)
		require.Nil(t, r)
	})
}

func TestPluginGeneralRuntimeQueueBudgetAndClose(t *testing.T) {
	life, cancel := context.WithCancel(context.Background())
	r := &generalPluginRuntime{life: life, cancel: cancel, slot: make(chan struct{}, 1), callTimeout: 40 * time.Millisecond}
	defer r.Close()
	_, release, err := r.begin(context.Background())
	require.NoError(t, err)
	started := time.Now()
	_, _, err = r.begin(context.Background())
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Less(t, time.Since(started), time.Second)
	require.NoError(t, r.life.Err(), "queue timeout must not retire the active instance")
	ctx, stop := context.WithCancel(context.Background())
	stop()
	_, _, err = r.begin(ctx)
	require.ErrorIs(t, err, context.Canceled)
	waiting := make(chan error, 1)
	go func() {
		_, done, e := r.begin(context.Background())
		if done != nil {
			done()
		}
		waiting <- e
	}()
	r.Close()
	select {
	case err := <-waiting:
		require.ErrorIs(t, err, ErrGeneralClosed)
	case <-time.After(time.Second):
		t.Fatal("queued call did not stop")
	}
	release()
	require.Empty(t, r.slot)
}

func TestPluginGeneralTaskUsesSchedulerDeadline(t *testing.T) {
	life, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := &generalPluginRuntime{life: life, cancel: cancel, slot: make(chan struct{}, 1), callTimeout: time.Millisecond}
	requested := time.Now().Add(time.Second)
	ctx, stop := context.WithDeadline(context.Background(), requested)
	defer stop()
	call, done, err := r.beginTask(ctx)
	require.NoError(t, err)
	defer done()
	deadline, ok := call.Deadline()
	require.True(t, ok)
	require.WithinDuration(t, requested, deadline, 5*time.Millisecond)
}
