package service

import (
	"fmt"
	"strings"

	pluginv1 "github.com/Wei-Shaw/sub2api/pkg/pluginapi/v1"
	"golang.org/x/mod/semver"
)

type PluginHostInfo struct {
	Version   string
	BuildType string
}

func EvaluatePluginCompatibility(manifest PluginManifest, host PluginHostInfo) PluginCompatibility {
	result := PluginCompatibility{
		GeneralAPI:         manifest.Requires.GeneralAPI,
		CurrentSub2API:     host.Version,
		RequiredSub2API:    manifest.Requires.Sub2API,
		RecommendedSub2API: manifest.Requires.RecommendedSub2APIVersion,
		PluginProtocol:     manifest.Requires.PluginProtocol,
		TransportAPI:       manifest.Requires.TransportAPI,
		UIBridge:           manifest.Requires.UIBridge,
	}
	if manifest.SchemaVersion == 2 {
		result.Status = "incompatible"
		if err := manifest.Validate(); err != nil {
			result.ReasonCode = "invalid_manifest"
			result.Message = err.Error()
		} else if !matchesSemverRange(host.Version, manifest.Requires.Sub2API) {
			result.ReasonCode = "host_version_mismatch"
			result.Message = "Sub2API version does not satisfy the general plugin requirement"
		} else if !pluginGatewayManifestSupported(manifest) {
			result.ReasonCode = "general_runtime_unavailable"
			result.Message = ErrPluginGeneralRuntimeUnavailable.Error()
		} else {
			result.Compatible = true
			for _, tested := range manifest.Requires.TestedSub2APIVersions {
				if normalizeSemver(tested) == normalizeSemver(host.Version) {
					result.Tested = true
					break
				}
			}
			result.Status = "untested"
			result.Message = "General hooks and independent tasks are supported; this host version is not declared tested"
			if result.Tested {
				result.Status = "compatible"
				result.Message = "General hooks and independent tasks are supported and this host version is declared tested"
			}
		}
		return result
	}
	if manifest.SchemaVersion != 1 || manifest.General != nil || manifest.Requires.GeneralAPI != 0 {
		result.Status = "incompatible"
		result.ReasonCode = "invalid_manifest"
		result.Message = "Unsupported or mixed plugin manifest schema"
		return result
	}
	if manifest.Requires.PluginProtocol != pluginv1.ProtocolVersion ||
		manifest.Requires.TransportAPI != pluginv1.TransportAPIVersion ||
		manifest.Requires.UIBridge != pluginv1.UIBridgeVersion {
		result.Status = "incompatible"
		result.Message = "插件协议版本与当前 Sub2API 不兼容"
		return result
	}
	if !matchesSemverRange(host.Version, manifest.Requires.Sub2API) {
		result.Status = "incompatible"
		result.Message = fmt.Sprintf("当前 Sub2API %s 不满足插件要求 %s", host.Version, manifest.Requires.Sub2API)
		return result
	}
	result.Compatible = true
	for _, tested := range manifest.Requires.TestedSub2APIVersions {
		if normalizeSemver(tested) == normalizeSemver(host.Version) {
			result.Tested = true
			break
		}
	}
	if result.Tested {
		result.Status = "compatible"
		result.Message = "当前 Sub2API 版本已由插件声明测试"
	} else {
		result.Status = "untested"
		result.Message = "版本范围兼容，但插件未声明已测试当前 Sub2API 版本"
	}
	return result
}

func normalizeSemver(version string) string {
	v := strings.TrimSpace(version)
	if v == "" {
		return ""
	}
	if !strings.HasPrefix(v, "v") {
		v = "v" + v
	}
	if !semver.IsValid(v) {
		return ""
	}
	return v
}

func matchesSemverRange(version, expression string) bool {
	v := normalizeSemver(version)
	if v == "" {
		return false
	}
	tokens := strings.Fields(strings.ReplaceAll(expression, ",", " "))
	if len(tokens) == 0 {
		return false
	}
	for _, token := range tokens {
		op := "="
		raw := token
		for _, candidate := range []string{">=", "<=", ">", "<", "="} {
			if strings.HasPrefix(token, candidate) {
				op = candidate
				raw = strings.TrimSpace(strings.TrimPrefix(token, candidate))
				break
			}
		}
		bound := normalizeSemver(raw)
		if bound == "" {
			return false
		}
		comparison := semver.Compare(v, bound)
		matched := map[string]bool{
			">=": comparison >= 0,
			"<=": comparison <= 0,
			">":  comparison > 0,
			"<":  comparison < 0,
			"=":  comparison == 0,
		}[op]
		if !matched {
			return false
		}
	}
	return true
}

// Only implemented hook protocols and task broker capabilities pass the runtime gate.
func pluginGatewayManifestSupported(manifest PluginManifest) bool {
	if manifest.General == nil || (len(manifest.General.Hooks) == 0 && len(manifest.General.Tasks) == 0) {
		return false
	}
	for _, capability := range manifest.General.HostCapabilities {
		switch capability {
		case "host.kv.v1", "host.network.read.v1", "host.channel.pricing.read.v1", "host.channel.pricing.write.v1":
			if len(manifest.General.Tasks) == 0 {
				return false
			}
		default:
			return false
		}
	}
	for _, hook := range manifest.General.Hooks {
		for _, protocol := range hook.Protocols {
			if !PluginGatewayProtocolSupported(protocol) {
				return false
			}
		}
	}
	return true
}
