// Test-only process fixture. Never installed as a production plugin.
package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	general "github.com/Wei-Shaw/sub2api/pkg/pluginapi/general/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type fixture struct {
	general.UnimplementedGeneralPluginServer
	mu           sync.Mutex
	configured   bool
	id, revision int64
	marker       string
	calls        atomic.Int64
}

func stalledInfoFixture(exe string) bool {
	if strings.Contains(filepath.Base(exe), "stall-info") {
		return true
	}
	_, err := os.Stat(filepath.Join(filepath.Dir(exe), "stall-info.marker"))
	return err == nil
}

func (f *fixture) GetInfo(ctx context.Context, _ *general.InfoRequest) (*general.InfoResponse, error) {
	exe, _ := os.Executable()
	if stalledInfoFixture(exe) {
		<-ctx.Done()
		return nil, status.FromContextError(ctx.Err()).Err()
	}
	return &general.InfoResponse{PluginId: "com.example.general", PluginVersion: "0.1.0", ProtocolVersion: 1, GeneralApiVersion: 1, ConfigVersion: 1, Capabilities: []string{"request.hook.v1", "independent.task.v1"}, Hooks: []*general.HookDeclaration{{Name: "inspect", Stages: []string{"input", "output"}, Protocols: []string{"example.text.v1"}, CanReplace: true}}, Tasks: []*general.TaskDeclaration{{Name: "sync_prices"}}}, nil
}
func (f *fixture) Configure(_ context.Context, r *general.ConfigureRequest) (*general.ConfigureResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.configured {
		return nil, status.Error(codes.FailedPrecondition, "secret reconfigure")
	}
	var config struct {
		Marker    string `json:"marker"`
		Reject    bool   `json:"reject"`
		WrongEcho bool   `json:"wrong_echo"`
	}
	if json.Unmarshal(r.ConfigJson, &config) != nil || config.Reject {
		return &general.ConfigureResponse{}, nil
	}
	f.configured = true
	f.id = r.InstanceId
	f.revision = r.ConfigRevision
	f.marker = config.Marker
	revision := r.ConfigRevision
	if config.WrongEcho {
		revision++
	}
	return &general.ConfigureResponse{Accepted: true, InstanceId: r.InstanceId, ConfigRevision: revision}, nil
}
func (f *fixture) identity(id, revision int64) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.configured && f.id == id && f.revision == revision
}
func (f *fixture) Check(ctx context.Context, r *general.HookRequest) (*general.HookResponse, error) {
	if !f.identity(r.InstanceId, r.ConfigRevision) {
		return nil, status.Error(codes.PermissionDenied, "secret identity")
	}
	f.calls.Add(1)
	switch string(r.GetContent().GetData()) {
	case "replace":
		return &general.HookResponse{Decision: "replace", Content: &general.Content{MediaType: "text/plain", Data: []byte(f.marker)}}, nil
	case "block":
		return &general.HookResponse{Decision: "block", Rejection: &general.Rejection{Code: "policy", Message: "Blocked"}, Findings: []*general.Finding{{RuleId: "r1", Confidence: &general.Finding_Score{Score: 0.01}}}}, nil
	case "invalid":
		return &general.HookResponse{Decision: "unknown"}, nil
	case "error":
		return nil, status.Error(codes.Internal, "secret runtime error")
	case "wire-oversize":
		return &general.HookResponse{Decision: "replace", Content: &general.Content{MediaType: "text/plain", Data: make([]byte, 3*1024*1024)}}, nil
	case "oversize":
		return &general.HookResponse{Decision: "replace", Content: &general.Content{MediaType: "text/plain", Data: make([]byte, 1024*1024+1)}}, nil
	case "ignore-cancel":
		select {}
	case "wait":
		<-ctx.Done()
		return nil, status.FromContextError(ctx.Err()).Err()
	default:
		return &general.HookResponse{Decision: "pass"}, nil
	}
}
func (f *fixture) RunTask(_ context.Context, r *general.TaskRequest) (*general.TaskResponse, error) {
	if !f.identity(r.InstanceId, r.ConfigRevision) {
		return nil, status.Error(codes.PermissionDenied, "secret identity")
	}
	if r.JobId == "partial" {
		return &general.TaskResponse{Status: "failed", Changed: true, ErrorCode: "partial_failure"}, nil
	}
	var data []byte
	if r.JobId == "count" {
		data, _ = json.Marshal(map[string]int64{"calls": f.calls.Load()})
	} else {
		data, _ = json.Marshal(map[string]string{"marker": f.marker})
	}
	return &general.TaskResponse{Status: "succeeded", OutputJson: data}, nil
}
func main() { general.Serve(&fixture{}) }
