// Package examples contains the source declarations used by the runtime and packager.
package examples

import (
	"embed"
	"encoding/json"
	"fmt"
	general "github.com/Wei-Shaw/sub2api/pkg/pluginapi/general/v1"
)

//go:embed credential-input/*.json business-output/*.json pricing-sync/*.json
var Sources embed.FS
var Names = []string{"credential-input", "business-output", "pricing-sync"}

func Source(name, file string) ([]byte, error) {
	for _, n := range Names {
		if name == n {
			return Sources.ReadFile(name + "/" + file)
		}
	}
	return nil, fmt.Errorf("unknown example")
}
func Info(name string) (*general.InfoResponse, error) {
	data, e := Source(name, "manifest.source.json")
	if e != nil {
		return nil, e
	}
	var m struct {
		ID       string `json:"id"`
		Version  string `json:"version"`
		Requires struct {
			Protocol uint32 `json:"plugin_protocol"`
			API      uint32 `json:"general_api"`
		} `json:"requires"`
		Capabilities []struct {
			ID string `json:"id"`
		} `json:"capabilities"`
		General struct {
			ConfigVersion int64 `json:"config_version"`
			Hooks         []struct {
				Name       string   `json:"name"`
				Stages     []string `json:"stages"`
				Protocols  []string `json:"protocols"`
				CanReplace bool     `json:"can_replace"`
			} `json:"hooks"`
			Tasks []struct {
				Name string `json:"name"`
			} `json:"tasks"`
			Host []string `json:"host_capabilities"`
		} `json:"general"`
	}
	if e = json.Unmarshal(data, &m); e != nil {
		return nil, e
	}
	r := &general.InfoResponse{PluginId: m.ID, PluginVersion: m.Version, ProtocolVersion: m.Requires.Protocol, GeneralApiVersion: m.Requires.API, ConfigVersion: m.General.ConfigVersion, HostCapabilities: m.General.Host}
	for _, c := range m.Capabilities {
		r.Capabilities = append(r.Capabilities, c.ID)
	}
	for _, h := range m.General.Hooks {
		r.Hooks = append(r.Hooks, &general.HookDeclaration{Name: h.Name, Stages: h.Stages, Protocols: h.Protocols, CanReplace: h.CanReplace})
	}
	for _, t := range m.General.Tasks {
		r.Tasks = append(r.Tasks, &general.TaskDeclaration{Name: t.Name})
	}
	return r, nil
}
