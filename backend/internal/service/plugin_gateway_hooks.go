package service

import (
	"context"
	"errors"
	"log/slog"
	"mime"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/internal/pluginflow"
	contract "github.com/Wei-Shaw/sub2api/pkg/pluginapi/contracts/v1"
)

var (
	ErrPluginGatewayBlocked  = errors.New("plugin gateway blocked")
	ErrPluginGatewayCheck    = errors.New("plugin gateway check failed")
	ErrPluginGatewayProtocol = errors.New("plugin gateway protocol unsupported")
)

// Sources provide pinned runtime leases, not an HTTP authorization boundary.
type PluginGatewayLease interface {
	Snapshot() (*PluginInstanceSnapshot, error)
	Check(context.Context, int64, contract.HookRequest) (contract.HookResult, error)
	Release()
}
type PluginGatewayLeaseSource interface {
	AcquireGroup(context.Context, int64, contract.Stage, string) ([]PluginGatewayLease, error)
}
type pluginGatewayInstanceSource struct{ instances *PluginInstanceService }

func (s pluginGatewayInstanceSource) AcquireGroup(ctx context.Context, id int64, stage contract.Stage, protocol string) ([]PluginGatewayLease, error) {
	leases, err := s.instances.AcquireGroup(ctx, id, stage, protocol)
	if err != nil {
		return nil, err
	}
	result := make([]PluginGatewayLease, len(leases))
	for i, l := range leases {
		result[i] = l
	}
	return result, nil
}

type PluginGatewayService struct{ source PluginGatewayLeaseSource }

func NewPluginGatewayService(instances *PluginInstanceService) *PluginGatewayService {
	if instances == nil {
		return &PluginGatewayService{}
	}
	return NewPluginGatewayServiceWithSource(pluginGatewayInstanceSource{instances})
}
func NewPluginGatewayServiceWithSource(source PluginGatewayLeaseSource) *PluginGatewayService {
	return &PluginGatewayService{source: source}
}

type pluginGatewayStage struct {
	bindings []pluginflow.Binding
	invokers map[int64]PluginGatewayLease
}
type PluginGatewayRequest struct {
	mu       sync.Mutex
	groupID  int64
	protocol string
	stages   map[contract.Stage]pluginGatewayStage
	leases   []PluginGatewayLease
	closed   bool
}

// Freeze BOTH stages before forwarding. A broken output binding never turns into
// an unbound request or starts an upstream request. Close releases all leases.
func (s *PluginGatewayService) Prepare(ctx context.Context, id int64, protocol string) (*PluginGatewayRequest, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if s == nil || s.source == nil || id <= 0 {
		return nil, ErrPluginGatewayCheck
	}
	p := &PluginGatewayRequest{groupID: id, protocol: protocol, stages: make(map[contract.Stage]pluginGatewayStage)}
	for _, stage := range []contract.Stage{contract.Input, contract.Output} {
		leases, err := s.source.AcquireGroup(ctx, id, stage, protocol)
		// Sources must normally clean partial errors; release returned leases too.
		p.leases = append(p.leases, leases...)
		if err != nil {
			p.Close()
			return nil, ErrPluginGatewayCheck
		}
		prepared := pluginGatewayStage{invokers: make(map[int64]PluginGatewayLease)}
		for _, lease := range leases {
			snapshot, err := lease.Snapshot()
			if err != nil || snapshot == nil {
				p.Close()
				return nil, ErrPluginGatewayCheck
			}
			for _, b := range snapshot.Bindings {
				if !b.Enabled || b.GroupID != id || b.Stage != stage {
					continue
				}
				if b.Protocol != protocol || b.ID <= 0 || b.InstanceID != snapshot.Instance.ID || !snapshot.Instance.Enabled || snapshot.Revision.Revision != snapshot.Instance.CurrentRevision {
					p.Close()
					return nil, ErrPluginGatewayCheck
				}
				if _, exists := prepared.invokers[b.ID]; exists {
					p.Close()
					return nil, ErrPluginGatewayCheck
				}
				prepared.bindings = append(prepared.bindings, pluginflow.Binding{ID: b.ID, InstanceID: b.InstanceID, GroupID: id, Stage: stage, Order: b.Order, Enabled: true, Ready: true, Version: snapshot.Installation.Version, ConfigRevision: snapshot.Revision.Revision, CanReplace: b.CanReplace})
				prepared.invokers[b.ID] = lease
			}
		}
		p.stages[stage] = prepared
	}
	if p.Bound() && !PluginGatewayProtocolSupported(protocol) {
		p.Close()
		return nil, ErrPluginGatewayProtocol
	}
	return p, nil
}
func (p *PluginGatewayRequest) Bound() bool {
	return p != nil && (len(p.stages[contract.Input].bindings) > 0 || p.HasOutput())
}
func (p *PluginGatewayRequest) HasOutput() bool {
	return p != nil && len(p.stages[contract.Output].bindings) > 0
}
func (p *PluginGatewayRequest) Close() {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return
	}
	p.closed = true
	for _, l := range p.leases {
		l.Release()
	}
}
func (p *PluginGatewayRequest) run(ctx context.Context, id string, stage contract.Stage, content contract.Content) (contract.Content, error) {
	if p == nil {
		return content.Clone(), nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return contract.Content{}, ErrPluginGatewayCheck
	}
	selected := p.stages[stage]
	req := contract.HookRequest{RequestID: id, AttemptID: id, GroupID: p.groupID, Stage: stage, Content: content}
	out, trace, err := pluginflow.RunWithTrace(ctx, req, selected.bindings, func(ctx context.Context, b pluginflow.Binding, r contract.HookRequest) (contract.HookResult, error) {
		result, err := selected.invokers[b.ID].Check(ctx, b.ID, r)
		if err != nil || result.Decision != contract.Replace || result.Content == nil {
			return result, err
		}
		// Reject before the next binding sees an invalid intermediate body. A later
		// plugin restoring model/cache controls does not legitimize the first edit.
		if stage == contract.Input {
			if result.Content.MediaType != "application/json" || ValidatePluginGatewayInput(p.protocol, r.Content.Data, result.Content.Data) != nil {
				return contract.HookResult{}, ErrPluginGatewayCheck
			}
		} else {
			media, params, parseErr := mime.ParseMediaType(result.Content.MediaType)
			if parseErr != nil || media != "text/plain" || (params["charset"] != "" && !strings.EqualFold(params["charset"], "utf-8")) || !utf8.Valid(result.Content.Data) {
				return contract.HookResult{}, ErrPluginGatewayCheck
			}
		}
		return result, nil
	})
	for _, record := range trace.Records {
		// Fixed identifiers/decisions only: never findings, inspected/replaced content,
		// rejection messages, credentials, configurations or foreign error strings.
		slog.Info("plugin gateway execution", "request_id", id, "group_id", record.GroupID, "instance_id", record.InstanceID, "binding_id", record.BindingID, "revision", record.ConfigRevision, "stage", record.Stage, "decision", record.Decision, "failure", record.Failure, "duration_ms", record.Duration.Milliseconds())
	}
	if err != nil {
		if ctx.Err() != nil {
			return contract.Content{}, ctx.Err()
		}
		return contract.Content{}, ErrPluginGatewayCheck
	}
	if out.Decision == contract.Block {
		return contract.Content{}, ErrPluginGatewayBlocked
	}
	if out.Content == nil {
		return contract.Content{}, ErrPluginGatewayCheck
	}
	return out.Content.Clone(), nil
}
func (p *PluginGatewayRequest) CheckInput(ctx context.Context, id string, body []byte) ([]byte, error) {
	if p == nil || len(p.stages[contract.Input].bindings) == 0 {
		return append([]byte(nil), body...), nil
	}
	out, err := p.run(ctx, id, contract.Input, contract.Content{MediaType: "application/json", Data: body})
	if err != nil {
		return nil, err
	}
	if out.MediaType != "application/json" || ValidatePluginGatewayInput(p.protocol, body, out.Data) != nil {
		return nil, ErrPluginGatewayCheck
	}
	return out.Data, nil
}
func (p *PluginGatewayRequest) CheckOutput(ctx context.Context, id string, content contract.Content) (contract.Content, error) {
	return p.run(ctx, id, contract.Output, content)
}

type pluginGatewayCacheBypassKey struct{}

// Until plugin bindings/revisions are part of the authoritative cache policy,
// binding ANY check denies only result caching, including reuse of an old HIT.
func WithPluginGatewayCacheBypass(ctx context.Context) context.Context {
	return context.WithValue(ctx, pluginGatewayCacheBypassKey{}, true)
}

type pluginGatewayEffectiveGroupKey struct{}

func WithPluginGatewayEffectiveGroup(ctx context.Context, groupID int64) context.Context {
	return context.WithValue(ctx, pluginGatewayEffectiveGroupKey{}, groupID)
}
func validatePluginGatewayEffectiveGroup(ctx context.Context, groupID int64) error {
	if expected, ok := ctx.Value(pluginGatewayEffectiveGroupKey{}).(int64); ok && expected != groupID {
		return ErrPluginGatewayCheck
	}
	return nil
}
