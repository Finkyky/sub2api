package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"slices"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pluginflow"
	contract "github.com/Wei-Shaw/sub2api/pkg/pluginapi/contracts/v1"
	general "github.com/Wei-Shaw/sub2api/pkg/pluginapi/general/v1"
	hclog "github.com/hashicorp/go-hclog"
	hcplugin "github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// One handle represents one immutable process/config revision. The future
// publisher must create a new handle rather than mutate this one in place.
type generalInstanceOptions struct {
	InstanceID     int64
	ConfigRevision int64
	Config         json.RawMessage
	Host           PluginHostInfo
	StartTimeout   time.Duration
	CallTimeout    time.Duration
	SocketDir      string
}

type generalPluginRuntime struct {
	client         *hcplugin.Client
	api            general.GeneralPluginClient
	manifest       PluginManifest
	instanceID     int64
	configRevision int64
	callTimeout    time.Duration
	life           context.Context
	cancel         context.CancelFunc
	closeOnce      sync.Once
	slot           chan struct{}
}

func generalObjectJSON(data []byte) bool {
	if len(data) == 0 || len(data) > generalJSONMaxBytes {
		return false
	}
	var object map[string]json.RawMessage
	return json.Unmarshal(data, &object) == nil && object != nil
}

func startGeneralPluginRuntime(ctx context.Context, installation *PluginInstallation, opt generalInstanceOptions) (*generalPluginRuntime, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if installation == nil || installation.Manifest.SchemaVersion != 2 || installation.Manifest.Validate() != nil || installation.PluginKey != installation.Manifest.ID || installation.Version != installation.Manifest.Version || !matchesSemverRange(opt.Host.Version, installation.Manifest.Requires.Sub2API) {
		return nil, ErrGeneralHandshake
	}
	if opt.InstanceID <= 0 || opt.ConfigRevision <= 0 || !generalObjectJSON(opt.Config) || opt.StartTimeout <= 0 || opt.CallTimeout <= 0 || opt.CallTimeout > 5*time.Minute {
		return nil, ErrGeneralConfig
	}
	checksum, err := hex.DecodeString(installation.BinarySHA256)
	if err != nil || len(checksum) != sha256.Size || installation.BinarySHA256 != installation.Manifest.Files[installation.Manifest.Runtimes[installation.Manifest.RuntimeKey()].Path] {
		return nil, ErrGeneralHandshake
	}
	// Deep-copy every declaration before launching: caller edits cannot change a
	// running instance's accepted entries, stages, protocols or version.
	encoded, err := json.Marshal(installation.Manifest)
	if err != nil {
		return nil, ErrGeneralHandshake
	}
	var manifest PluginManifest
	if json.Unmarshal(encoded, &manifest) != nil {
		return nil, ErrGeneralHandshake
	}
	config := bytes.Clone(opt.Config)
	life, cancel := context.WithCancel(context.Background())
	r := &generalPluginRuntime{manifest: manifest, instanceID: opt.InstanceID, configRevision: opt.ConfigRevision, callTimeout: opt.CallTimeout, life: life, cancel: cancel, slot: make(chan struct{}, 1)}
	cmd := exec.CommandContext(life, installation.BinaryPath)
	r.client = hcplugin.NewClient(&hcplugin.ClientConfig{HandshakeConfig: general.HandshakeConfig, Plugins: general.ClientPluginMap(), Cmd: cmd, AllowedProtocols: []hcplugin.Protocol{hcplugin.ProtocolGRPC}, StartTimeout: opt.StartTimeout, SecureConfig: &hcplugin.SecureConfig{Checksum: checksum, Hash: sha256.New()}, Logger: hclog.NewNullLogger(), SyncStdout: io.Discard, SyncStderr: io.Discard, SkipHostEnv: true, UnixSocketConfig: &hcplugin.UnixSocketConfig{TempDir: opt.SocketDir}, GRPCDialOptions: []grpc.DialOption{grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(general.MaxMessageBytes), grpc.MaxCallSendMsgSize(general.MaxMessageBytes))}})
	startup, stop := context.WithTimeout(ctx, opt.StartTimeout)
	defer stop()
	// Also abort process startup (before a gRPC connection exists) on cancellation.
	abort := context.AfterFunc(startup, cancel)
	committed := false
	defer func() {
		abort()
		if !committed {
			r.Close()
		}
	}()
	rpc, err := r.client.Client()
	if err != nil {
		return nil, normalizeGeneralError(startup, err)
	}
	dispensed, err := rpc.Dispense(general.PluginName)
	if err != nil {
		return nil, normalizeGeneralError(startup, err)
	}
	api, ok := dispensed.(general.GeneralPluginClient)
	if !ok {
		return nil, ErrGeneralHandshake
	}
	r.api = api
	info, err := api.GetInfo(startup, &general.InfoRequest{})
	if err != nil {
		return nil, normalizeGeneralError(startup, err)
	}
	if err := validateGeneralRuntimeInfo(manifest, info); err != nil {
		return nil, err
	}
	configured, err := api.Configure(startup, &general.ConfigureRequest{InstanceId: opt.InstanceID, ConfigRevision: opt.ConfigRevision, ConfigVersion: int64(manifest.General.ConfigVersion), ConfigJson: config})
	if err != nil {
		return nil, normalizeGeneralError(startup, err)
	}
	if configured == nil || !configured.Accepted || configured.InstanceId != opt.InstanceID || configured.ConfigRevision != opt.ConfigRevision {
		return nil, ErrGeneralConfig
	}
	if err := startup.Err(); err != nil {
		return nil, err
	}
	if !abort() {
		return nil, normalizeGeneralError(startup, context.Canceled)
	}
	committed = true
	return r, nil
}

func normalizeGeneralError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	switch status.Code(err) {
	case codes.Canceled:
		return context.Canceled
	case codes.DeadlineExceeded:
		return context.DeadlineExceeded
	}
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	return ErrGeneralInvocation
}

// A returned cancellation does not prove the remote handler stopped. Retire
// the isolated instance before releasing the slot; never retry a side effect.
func (r *generalPluginRuntime) finishRPC(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	if err != nil {
		normalized := normalizeGeneralError(ctx, err)
		r.Close()
		return normalized
	}
	if r.life.Err() != nil {
		return ErrGeneralClosed
	}
	return nil
}

// Close is idempotent; it cancels queued/in-flight operations and kills the
// isolated process. Graceful publication/draining is the P2c owner's concern.
func (r *generalPluginRuntime) Close() {
	if r == nil {
		return
	}
	r.closeOnce.Do(func() {
		r.cancel()
		if r.client != nil {
			r.client.Kill()
		}
	})
}

func (r *generalPluginRuntime) begin(ctx context.Context) (context.Context, func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if r.life.Err() != nil {
		return nil, nil, ErrGeneralClosed
	}
	call, cancel := context.WithTimeout(ctx, r.callTimeout)
	abort := context.AfterFunc(r.life, cancel)
	cleanup := func() { abort(); cancel() }
	select {
	case r.slot <- struct{}{}:
	case <-call.Done():
		cleanup()
		if r.life.Err() != nil {
			return nil, nil, ErrGeneralClosed
		}
		return nil, nil, call.Err()
	}
	if err := call.Err(); err != nil {
		<-r.slot
		cleanup()
		if r.life.Err() != nil {
			return nil, nil, ErrGeneralClosed
		}
		return nil, nil, err
	}
	if r.life.Err() != nil {
		<-r.slot
		cleanup()
		return nil, nil, ErrGeneralClosed
	}
	return call, func() { <-r.slot; cleanup() }, nil
}

func (r *generalPluginRuntime) HookInvoker(entry, protocol string) pluginflow.Invoker {
	return func(ctx context.Context, b pluginflow.Binding, req contract.HookRequest) (contract.HookResult, error) {
		if b.ID <= 0 || !b.Enabled || !b.Ready || b.InstanceID != r.instanceID || b.ConfigRevision != r.configRevision || b.Version != r.manifest.Version || b.GroupID <= 0 || b.GroupID != req.GroupID || b.Stage != req.Stage || !req.Stage.Valid() {
			return contract.HookResult{}, ErrGeneralBinding
		}
		var declaration *PluginHookEntry
		for i := range r.manifest.General.Hooks {
			h := &r.manifest.General.Hooks[i]
			if h.Name == entry && slices.Contains(h.Protocols, protocol) && slices.Contains(h.Stages, req.Stage) {
				declaration = h
				break
			}
		}
		if declaration == nil {
			return contract.HookResult{}, ErrGeneralBinding
		}
		if len(req.Content.Data) > generalBodyMaxBytes {
			return contract.HookResult{}, ErrGeneralResult
		}
		call, done, err := r.begin(ctx)
		if err != nil {
			return contract.HookResult{}, err
		}
		defer done()
		response, err := r.api.Check(call, &general.HookRequest{InstanceId: r.instanceID, ConfigRevision: r.configRevision, Entry: entry, Protocol: protocol, RequestId: req.RequestID, AttemptId: req.AttemptID, GroupId: req.GroupID, Stage: string(req.Stage), Content: &general.Content{MediaType: req.Content.MediaType, Data: bytes.Clone(req.Content.Data)}})
		if err = r.finishRPC(call, err); err != nil {
			return contract.HookResult{}, err
		}
		return decodeGeneralHookResult(response, b.CanReplace && declaration.CanReplace)
	}
}

func (r *generalPluginRuntime) RunTask(ctx context.Context, entry, jobID string, input json.RawMessage) (contract.TaskResult, error) {
	found := false
	for _, t := range r.manifest.General.Tasks {
		if t.Name == entry {
			found = true
			break
		}
	}
	if !found || jobID == "" {
		return contract.TaskResult{}, ErrGeneralBinding
	}
	if !generalObjectJSON(input) {
		return contract.TaskResult{}, ErrGeneralConfig
	}
	call, done, err := r.beginTask(ctx)
	if err != nil {
		return contract.TaskResult{}, err
	}
	defer done()
	response, err := r.api.RunTask(call, &general.TaskRequest{InstanceId: r.instanceID, ConfigRevision: r.configRevision, Entry: entry, JobId: jobID, InputJson: bytes.Clone(input)})
	if err = r.finishRPC(call, err); err != nil {
		return contract.TaskResult{}, err
	}
	return decodeGeneralTaskResult(response)
}
