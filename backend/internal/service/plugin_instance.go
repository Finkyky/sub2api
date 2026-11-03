package service

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	contract "github.com/Wei-Shaw/sub2api/pkg/pluginapi/contracts/v1"
)

var (
	ErrPluginInstanceEnabled     = errors.New("plugin instance is enabled")
	ErrPluginInstanceBusy        = errors.New("plugin instance has active local leases")
	ErrPluginInstanceInvalid     = errors.New("invalid plugin instance or binding")
	ErrPluginInstanceConflict    = errors.New("plugin instance generation changed")
	ErrPluginInstanceNotFound    = errors.New("plugin instance or revision not found")
	ErrPluginInstanceUnavailable = errors.New("bound plugin instance unavailable")
)

// Generation is the CAS token for all management writes. Revision identifies
// immutable encrypted configuration, not the mutable publication generation.
type PluginInstance struct {
	ID              int64  `json:"id"`
	PluginID        int64  `json:"plugin_id"`
	Name            string `json:"name"`
	Generation      int64  `json:"generation"`
	CurrentRevision int64  `json:"current_revision"`
	LatestRevision  int64  `json:"latest_revision"`
	Enabled         bool   `json:"enabled"`
}
type PluginInstanceRevision struct {
	InstanceID      int64  `json:"instance_id"`
	Revision        int64  `json:"revision"`
	ConfigVersion   int    `json:"config_version"`
	ConfigEncrypted string `json:"-"`
}
type PluginInstanceRevisionSummary struct {
	InstanceID    int64 `json:"instance_id"`
	Revision      int64 `json:"revision"`
	ConfigVersion int   `json:"config_version"`
}
type PluginGroupBinding struct {
	ID         int64          `json:"id"`
	InstanceID int64          `json:"instance_id"`
	GroupID    int64          `json:"group_id"`
	Stage      contract.Stage `json:"stage"`
	Entry      string         `json:"entry"`
	Protocol   string         `json:"protocol"`
	Order      int            `json:"sort_order"`
	Enabled    bool           `json:"enabled"`
	CanReplace bool           `json:"can_replace"`
}

// Snapshot is an internal service projection. Installation contains private
// paths but no plaintext config; HTTP DTOs must explicitly project their fields.
type PluginInstanceSnapshot struct {
	Instance     PluginInstance         `json:"instance"`
	Revision     PluginInstanceRevision `json:"revision"`
	Installation PluginInstallation     `json:"-"`
	Bindings     []PluginGroupBinding   `json:"bindings"`
}
type PluginInstanceRepository interface {
	// Delete locks database authority and enforces generation CAS and disabled.
	// The service is responsible for this node's active lease check.
	Delete(context.Context, int64, int64) error
	Create(context.Context, int64, string, string) (*PluginInstanceSnapshot, error)
	List(context.Context, int64) ([]PluginInstance, error)
	Snapshot(context.Context, int64, int64) (*PluginInstanceSnapshot, error)
	RevisionSummaries(context.Context, int64) ([]PluginInstanceRevisionSummary, error)
	SaveRevision(context.Context, int64, int64, string) (*PluginInstanceSnapshot, error)
	Publish(context.Context, int64, int64, int64, bool) (*PluginInstanceSnapshot, error)
	ReplaceBindings(context.Context, int64, int64, []PluginGroupBinding) (*PluginInstanceSnapshot, error)
	GroupSnapshots(context.Context, int64, contract.Stage, string) ([]PluginInstanceSnapshot, error)
}

func ValidatePluginGroupBindings(manifest PluginManifest, instanceID int64, bindings []PluginGroupBinding) error {
	if instanceID <= 0 || manifest.SchemaVersion != 2 || manifest.General == nil || len(bindings) > 128 {
		return ErrPluginInstanceInvalid
	}
	seen := make(map[string]bool)
	for _, b := range bindings {
		if b.ID < 0 || b.InstanceID != instanceID || b.GroupID <= 0 || !b.Stage.Valid() || b.Order < -100000 || b.Order > 100000 {
			return ErrPluginInstanceInvalid
		}
		found := false
		for _, h := range manifest.General.Hooks {
			if h.Name == b.Entry && slices.Contains(h.Stages, b.Stage) && slices.Contains(h.Protocols, b.Protocol) && (!b.CanReplace || h.CanReplace) {
				found = true
				break
			}
		}
		if !found {
			return ErrPluginInstanceInvalid
		}
		// JSON encodes the entire unique tuple, avoiding ambiguous delimiter joins.
		key, _ := json.Marshal([]any{b.GroupID, b.Stage, b.Entry, b.Protocol})
		if seen[string(key)] {
			return ErrPluginInstanceInvalid
		}
		seen[string(key)] = true
	}
	return nil
}
func ValidatePluginInstanceCreate(pluginID int64, name, encrypted string) error {
	if pluginID <= 0 || strings.TrimSpace(name) == "" || len(name) > 160 || encrypted == "" || len(encrypted) > 256*1024 {
		return ErrPluginInstanceInvalid
	}
	return nil
}
func clonePluginInstanceSnapshot(s *PluginInstanceSnapshot) (*PluginInstanceSnapshot, error) {
	if s == nil {
		return nil, ErrPluginInstanceInvalid
	}
	copy := *s
	data, err := json.Marshal(s.Installation.Manifest)
	if err != nil {
		return nil, err
	}
	copy.Installation.Manifest = PluginManifest{}
	if err = json.Unmarshal(data, &copy.Installation.Manifest); err != nil {
		return nil, err
	}
	copy.Installation.ArtifactData = nil
	if s.Installation.InstalledBy != nil {
		v := *s.Installation.InstalledBy
		copy.Installation.InstalledBy = &v
	}
	if s.Installation.EnabledAt != nil {
		v := *s.Installation.EnabledAt
		copy.Installation.EnabledAt = &v
	}
	copy.Installation.Bindings = append([]PluginBinding(nil), s.Installation.Bindings...)
	copy.Bindings = append([]PluginGroupBinding(nil), s.Bindings...)
	return &copy, nil
}
