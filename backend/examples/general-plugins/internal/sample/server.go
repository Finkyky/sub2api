// Package sample implements bounded, credential-free general.v1 examples.
package sample

import (
	"context"
	examples "github.com/Wei-Shaw/sub2api/examples/general-plugins"
	general "github.com/Wei-Shaw/sub2api/pkg/pluginapi/general/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"sync"
)

type Server struct {
	general.UnimplementedGeneralPluginServer
	name               string
	info               *general.InfoResponse
	mu                 sync.RWMutex
	configured         bool
	instance, revision int64
	credentials        credentialConfig
	business           businessConfig
	pricing            pricingConfig
}

func New(name string) (*Server, error) {
	info, e := examples.Info(name)
	if e != nil {
		return nil, e
	}
	return &Server{name: name, info: info}, nil
}
func canceled(ctx context.Context) error {
	if e := ctx.Err(); e != nil {
		return status.FromContextError(e).Err()
	}
	return nil
}
func invalid(ctx context.Context) error {
	if e := canceled(ctx); e != nil {
		return e
	}
	return status.Error(codes.InvalidArgument, "invalid plugin input")
}
func limit() error { return status.Error(codes.ResourceExhausted, "plugin size limit exceeded") }
func (s *Server) GetInfo(ctx context.Context, _ *general.InfoRequest) (*general.InfoResponse, error) {
	if e := canceled(ctx); e != nil {
		return nil, e
	}
	return proto.Clone(s.info).(*general.InfoResponse), nil
}
func (s *Server) Configure(ctx context.Context, r *general.ConfigureRequest) (*general.ConfigureResponse, error) {
	if e := canceled(ctx); e != nil {
		return nil, e
	}
	if r == nil {
		return nil, invalid(ctx)
	}
	if len(r.ConfigJson) > MaxJSONBytes {
		return nil, limit()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.configured {
		return nil, status.Error(codes.FailedPrecondition, "immutable plugin configuration")
	}
	if r.InstanceId <= 0 || r.ConfigRevision <= 0 || r.ConfigVersion != s.info.ConfigVersion {
		return nil, invalid(ctx)
	}
	if e := s.loadConfig(ctx, r.ConfigJson); e != nil {
		return nil, invalid(ctx)
	}
	if e := canceled(ctx); e != nil {
		return nil, e
	}
	s.instance = r.InstanceId
	s.revision = r.ConfigRevision
	s.configured = true
	return &general.ConfigureResponse{Accepted: true, InstanceId: s.instance, ConfigRevision: s.revision}, nil
}
func (s *Server) binding(id, rev int64) error {
	if !s.configured || s.instance != id || s.revision != rev {
		return status.Error(codes.FailedPrecondition, "plugin binding mismatch")
	}
	return nil
}
func (s *Server) Check(ctx context.Context, r *general.HookRequest) (*general.HookResponse, error) {
	if e := canceled(ctx); e != nil {
		return nil, e
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.info.Hooks) == 0 {
		return nil, status.Error(codes.Unimplemented, "hook not declared")
	}
	if r == nil {
		return nil, invalid(ctx)
	}
	if e := s.binding(r.InstanceId, r.ConfigRevision); e != nil {
		return nil, e
	}
	h := s.info.Hooks[0]
	validProtocol := false
	for _, p := range h.Protocols {
		validProtocol = validProtocol || p == r.Protocol
	}
	if r.Entry != h.Name || r.Stage != h.Stages[0] || !validProtocol || r.Content == nil {
		return nil, invalid(ctx)
	}
	if len(r.Content.Data) > MaxContentBytes {
		return nil, limit()
	}
	text, e := hookText(ctx, r)
	if e != nil {
		return nil, invalid(ctx)
	}
	res := &general.HookResponse{Decision: "pass"}
	if s.name == "credential-input" {
		for _, rule := range s.credentials.Rules {
			if e := canceled(ctx); e != nil {
				return nil, e
			}
			if rule.compiled.MatchString(text) {
				res.Decision = "block"
				res.Rejection = &general.Rejection{Code: "credential_detected", Message: "Credential-like content is blocked."}
				res.Findings = append(res.Findings, &general.Finding{RuleId: rule.ID, RuleVersion: rule.Version, Category: "credential"})
			}
		}
	} else {
		res = s.businessCheck(ctx, text)
	}
	if e := canceled(ctx); e != nil {
		return nil, e
	}
	return res, nil
}
func (s *Server) RunTask(ctx context.Context, r *general.TaskRequest) (*general.TaskResponse, error) {
	if e := canceled(ctx); e != nil {
		return nil, e
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.info.Tasks) == 0 {
		return nil, status.Error(codes.Unimplemented, "task not declared")
	}
	if r == nil {
		return nil, invalid(ctx)
	}
	if e := s.binding(r.InstanceId, r.ConfigRevision); e != nil {
		return nil, e
	}
	if r.Entry != s.info.Tasks[0].Name {
		return nil, invalid(ctx)
	}
	if len(r.InputJson) > MaxJSONBytes {
		return nil, limit()
	}
	return s.runPricing(ctx, r.InputJson)
}
