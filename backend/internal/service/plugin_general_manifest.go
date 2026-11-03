package service

import (
	"errors"
	"fmt"
	"regexp"

	contract "github.com/Wei-Shaw/sub2api/pkg/pluginapi/contracts/v1"
	general "github.com/Wei-Shaw/sub2api/pkg/pluginapi/general/v1"
	pluginv1 "github.com/Wei-Shaw/sub2api/pkg/pluginapi/v1"
)

const (
	PluginCapabilityRequestHook     = "request.hook.v1"
	PluginCapabilityIndependentTask = "independent.task.v1"
	PluginGeneralAPIVersion         = general.APIVersion
)

var ErrPluginGeneralRuntimeUnavailable = errors.New("通用插件实例管理入口尚未接入，当前管理界面仅支持安装存储")
var pluginEntryNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)

// PluginGeneralManifest declares entries and requested host capabilities.
// Declarations are not grants. Instance authorization and supported protocol
// matching must be enforced by the future general runtime and gateway adapters.
type PluginGeneralManifest struct {
	ConfigVersion    int               `json:"config_version"`
	Hooks            []PluginHookEntry `json:"hooks,omitempty"`
	Tasks            []PluginTaskEntry `json:"tasks,omitempty"`
	HostCapabilities []string          `json:"host_capabilities,omitempty"`
}

type PluginHookEntry struct {
	Name       string           `json:"name"`
	Stages     []contract.Stage `json:"stages"`
	Protocols  []string         `json:"protocols"`
	CanReplace bool             `json:"can_replace,omitempty"`
}

type PluginTaskEntry struct {
	Name string `json:"name"`
}

func (m PluginManifest) validateGeneralDeclaration() error {
	if m.General == nil {
		return errors.New("general plugin declaration is required")
	}
	if m.Requires.PluginProtocol != pluginv1.ProtocolVersion || m.Requires.GeneralAPI != PluginGeneralAPIVersion || m.Requires.TransportAPI != 0 {
		return errors.New("general plugin protocol is incompatible or mixed with transport API")
	}
	if (m.UI.Entrypoint == "" && m.Requires.UIBridge != 0) || (m.UI.Entrypoint != "" && m.Requires.UIBridge != pluginv1.UIBridgeVersion) {
		return errors.New("general plugin UI bridge does not match its UI declaration")
	}
	g := m.General
	if g.ConfigVersion <= 0 {
		return errors.New("general plugin config_version must be positive")
	}
	if len(m.Capabilities) == 0 || len(m.Capabilities) > 2 {
		return errors.New("general plugin requires one or two execution capabilities")
	}
	capabilities := make(map[string]bool, len(m.Capabilities))
	for _, c := range m.Capabilities {
		if (c.ID != PluginCapabilityRequestHook && c.ID != PluginCapabilityIndependentTask) || c.Platform != "" || c.AccountType != "" || capabilities[c.ID] {
			return errors.New("invalid or duplicate general plugin capability")
		}
		capabilities[c.ID] = true
	}
	if len(g.Hooks) > 32 || len(g.Tasks) > 32 {
		return errors.New("too many general plugin entries")
	}
	if capabilities[PluginCapabilityRequestHook] != (len(g.Hooks) > 0) || capabilities[PluginCapabilityIndependentTask] != (len(g.Tasks) > 0) {
		return errors.New("general plugin capabilities and entries must match")
	}
	names := make(map[string]bool, len(g.Hooks)+len(g.Tasks))
	checkName := func(name string) error {
		if !pluginEntryNamePattern.MatchString(name) || names[name] {
			return errors.New("invalid or duplicate general plugin entry name")
		}
		names[name] = true
		return nil
	}
	for _, hook := range g.Hooks {
		if err := checkName(hook.Name); err != nil {
			return err
		}
		if len(hook.Stages) == 0 || len(hook.Stages) > 2 {
			return errors.New("hook must declare one or two stages")
		}
		stages := make(map[contract.Stage]bool, len(hook.Stages))
		for _, stage := range hook.Stages {
			if !stage.Valid() || stages[stage] {
				return errors.New("invalid or duplicate hook stage")
			}
			stages[stage] = true
		}
		if err := validateGeneralIdentifiers(hook.Protocols, 16, true); err != nil {
			return fmt.Errorf("hook protocols: %w", err)
		}
	}
	for _, task := range g.Tasks {
		if err := checkName(task.Name); err != nil {
			return err
		}
	}
	if err := validateGeneralIdentifiers(g.HostCapabilities, 32, false); err != nil {
		return fmt.Errorf("requested host capabilities: %w", err)
	}
	return nil
}

func validateGeneralIdentifiers(values []string, max int, required bool) error {
	if len(values) > max || (required && len(values) == 0) {
		return errors.New("invalid declaration count")
	}
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		if len(value) > 160 || !pluginIDPattern.MatchString(value) || seen[value] {
			return errors.New("invalid or duplicate namespace identifier")
		}
		seen[value] = true
	}
	return nil
}
