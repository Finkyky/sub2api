package service

import (
	"bytes"
	"errors"
	"sort"

	contract "github.com/Wei-Shaw/sub2api/pkg/pluginapi/contracts/v1"
	general "github.com/Wei-Shaw/sub2api/pkg/pluginapi/general/v1"
)

const (
	generalBodyMaxBytes = 1024 * 1024
	generalJSONMaxBytes = 64 * 1024
)

var (
	ErrGeneralHandshake  = errors.New("general plugin handshake mismatch")
	ErrGeneralConfig     = errors.New("general plugin configuration rejected")
	ErrGeneralBinding    = errors.New("general plugin binding mismatch")
	ErrGeneralResult     = errors.New("invalid general plugin result")
	ErrGeneralInvocation = errors.New("general plugin invocation failed")
	ErrGeneralClosed     = errors.New("general plugin instance closed")
)

func sameGeneralSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	aa := append([]string(nil), a...)
	bb := append([]string(nil), b...)
	sort.Strings(aa)
	sort.Strings(bb)
	for i := range aa {
		if aa[i] != bb[i] || (i > 0 && (aa[i] == aa[i-1] || bb[i] == bb[i-1])) {
			return false
		}
	}
	return true
}

func validateGeneralRuntimeInfo(m PluginManifest, info *general.InfoResponse) error {
	if info == nil || m.General == nil || info.PluginId != m.ID || info.PluginVersion != m.Version || info.ProtocolVersion != general.ProtocolVersion || info.GeneralApiVersion != general.APIVersion || info.ConfigVersion != int64(m.General.ConfigVersion) {
		return ErrGeneralHandshake
	}
	caps := make([]string, 0, len(m.Capabilities))
	for _, c := range m.Capabilities {
		caps = append(caps, c.ID)
	}
	if !sameGeneralSet(caps, info.Capabilities) || !sameGeneralSet(m.General.HostCapabilities, info.HostCapabilities) || len(m.General.Hooks) != len(info.Hooks) || len(m.General.Tasks) != len(info.Tasks) {
		return ErrGeneralHandshake
	}
	hooks := make(map[string]PluginHookEntry, len(m.General.Hooks))
	for _, h := range m.General.Hooks {
		hooks[h.Name] = h
	}
	for _, h := range info.Hooks {
		if h == nil {
			return ErrGeneralHandshake
		}
		expected, ok := hooks[h.Name]
		if !ok {
			return ErrGeneralHandshake
		}
		stages := make([]string, 0, len(expected.Stages))
		for _, s := range expected.Stages {
			stages = append(stages, string(s))
		}
		if h.CanReplace != expected.CanReplace || !sameGeneralSet(h.Stages, stages) || !sameGeneralSet(h.Protocols, expected.Protocols) {
			return ErrGeneralHandshake
		}
		delete(hooks, h.Name)
	}
	tasks := make(map[string]bool, len(m.General.Tasks))
	for _, task := range m.General.Tasks {
		tasks[task.Name] = true
	}
	for _, task := range info.Tasks {
		if task == nil || !tasks[task.Name] {
			return ErrGeneralHandshake
		}
		delete(tasks, task.Name)
	}
	return nil
}

func decodeGeneralHookResult(wire *general.HookResponse, canReplace bool) (contract.HookResult, error) {
	if wire == nil || len(wire.Findings) > 128 {
		return contract.HookResult{}, ErrGeneralResult
	}
	out := contract.HookResult{Decision: contract.Decision(wire.Decision)}
	if wire.Content != nil {
		if len(wire.Content.Data) > generalBodyMaxBytes {
			return contract.HookResult{}, ErrGeneralResult
		}
		out.Content = &contract.Content{MediaType: wire.Content.MediaType, Data: bytes.Clone(wire.Content.Data)}
	}
	if wire.Rejection != nil {
		out.Rejection = &contract.Rejection{Code: wire.Rejection.Code, Message: wire.Rejection.Message}
	}
	for _, f := range wire.Findings {
		if f == nil {
			return contract.HookResult{}, ErrGeneralResult
		}
		finding := contract.Finding{RuleID: f.RuleId, RuleVersion: f.RuleVersion, Category: f.Category}
		if score, ok := f.Confidence.(*general.Finding_Score); ok {
			v := score.Score
			finding.Confidence = &v
		}
		out.Findings = append(out.Findings, finding)
	}
	if contract.ValidateHookResult(out, canReplace) != nil {
		return contract.HookResult{}, ErrGeneralResult
	}
	return out, nil
}

func decodeGeneralTaskResult(wire *general.TaskResponse) (contract.TaskResult, error) {
	if wire == nil || len(wire.OutputJson) > generalJSONMaxBytes {
		return contract.TaskResult{}, ErrGeneralResult
	}
	out := contract.TaskResult{Status: contract.TaskStatus(wire.Status), Changed: wire.Changed, ErrorCode: wire.ErrorCode, Output: bytes.Clone(wire.OutputJson)}
	if contract.ValidateTaskResult(out) != nil {
		return contract.TaskResult{}, ErrGeneralResult
	}
	return out, nil
}
