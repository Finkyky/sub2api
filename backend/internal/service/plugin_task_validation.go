package service

import (
	"encoding/json"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
)

const (
	PluginTaskMaxInputBytes  = 32 * 1024
	PluginTaskMaxResultBytes = generalJSONMaxBytes + 1024
	PluginTaskMaxTimeout     = 5 * time.Minute
	PluginTaskPollLimit      = 64
)

// PluginTaskNextRun accepts exactly five UTC fields or @every >= 1 minute.
// Timezone directives and calendar aliases are deliberately not accepted.
func PluginTaskNextRun(spec string, after time.Time) (time.Time, error) {
	spec = strings.TrimSpace(spec)
	var schedule cron.Schedule
	var err error
	if strings.HasPrefix(spec, "@every ") {
		duration, e := time.ParseDuration(strings.TrimSpace(strings.TrimPrefix(spec, "@every ")))
		if e != nil || duration < time.Minute || duration > 365*24*time.Hour {
			return time.Time{}, ErrPluginTaskInvalid
		}
		schedule = cron.Every(duration)
	} else {
		if len(strings.Fields(spec)) != 5 || strings.Contains(spec, "=") {
			return time.Time{}, ErrPluginTaskInvalid
		}
		schedule, err = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow).Parse(spec)
		if err != nil {
			return time.Time{}, ErrPluginTaskInvalid
		}
	}
	next := schedule.Next(after.UTC()).UTC()
	if next.IsZero() || !next.After(after) {
		return time.Time{}, ErrPluginTaskInvalid
	}
	return next, nil
}

// Input is public business JSON, not the encrypted instance configuration.
func ValidatePluginTaskDefinition(task PluginTask) error {
	if task.InstanceID <= 0 || strings.TrimSpace(task.Name) == "" || len(task.Name) > 160 || !pluginEntryNamePattern.MatchString(task.Entry) || len(task.Cron) > 256 || task.TimeoutSeconds < 1 || task.TimeoutSeconds > 300 || task.MaxRetries < 0 || task.MaxRetries > 10 || task.RetryBackoffSeconds < 1 || task.RetryBackoffSeconds > 3600 {
		return ErrPluginTaskInvalid
	}
	if len(task.Input) == 0 || len(task.Input) > PluginTaskMaxInputBytes || !generalObjectJSON(task.Input) {
		return ErrPluginTaskInvalid
	}
	if task.Cron != "" {
		if _, e := PluginTaskNextRun(task.Cron, time.Now().UTC()); e != nil {
			return e
		}
	}
	scope := task.Scope
	if len(scope.ChannelIDs) > 128 || len(scope.PricingFields) > 5 || len(scope.NetworkURLs) > 8 || len(scope.KVKeys) > 128 {
		return ErrPluginTaskInvalid
	}
	ids := map[int64]bool{}
	for _, id := range scope.ChannelIDs {
		if id <= 0 || ids[id] {
			return ErrPluginTaskInvalid
		}
		ids[id] = true
	}
	for _, fields := range [][]string{scope.PricingFields, scope.NetworkURLs, scope.KVKeys} {
		seen := map[string]bool{}
		for _, field := range fields {
			if strings.TrimSpace(field) == "" || len(field) > 2048 || seen[field] || strings.ContainsAny(field, "\x00\r\n") {
				return ErrPluginTaskInvalid
			}
			seen[field] = true
		}
	}
	for _, key := range scope.KVKeys {
		if len(key) > 160 || strings.TrimSpace(key) != key {
			return ErrPluginTaskInvalid
		}
	}
	for _, field := range scope.PricingFields {
		if len(field) > 64 {
			return ErrPluginTaskInvalid
		}
	}
	for _, raw := range scope.NetworkURLs {
		u, e := url.Parse(raw)
		if e != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
			return ErrPluginTaskInvalid
		}
	}
	return nil
}

// ValidatePluginTaskEntry is repository-level structural/entry validation.
// Repository authority does not grant host capabilities.
func ValidatePluginTaskEntry(task PluginTask, manifest PluginManifest) error {
	if e := ValidatePluginTaskDefinition(task); e != nil {
		return e
	}
	if manifest.SchemaVersion != 2 || manifest.General == nil {
		return ErrPluginTaskInvalid
	}
	capability := false
	for _, c := range manifest.Capabilities {
		if c.ID == PluginCapabilityIndependentTask {
			capability = true
		}
	}
	if !capability {
		return ErrPluginTaskCapability
	}
	if !slices.ContainsFunc(manifest.General.Tasks, func(e PluginTaskEntry) bool { return e.Name == task.Entry }) {
		return ErrPluginTaskInvalid
	}
	return nil
}

// ValidatePluginTask additionally rejects administrator scope not declared by
// the instance manifest. Prepare/Commit independently validate and grant it.
func ValidatePluginTask(task PluginTask, manifest PluginManifest) error {
	if e := ValidatePluginTaskEntry(task, manifest); e != nil {
		return e
	}
	return ValidatePluginTaskCapabilities(task, manifest)
}

func pluginTaskClone(t *PluginTask) (*PluginTask, error) {
	if t == nil {
		return nil, ErrPluginTaskInvalid
	}
	data, e := json.Marshal(t)
	if e != nil {
		return nil, ErrPluginTaskInvalid
	}
	var copy PluginTask
	if json.Unmarshal(data, &copy) != nil {
		return nil, ErrPluginTaskInvalid
	}
	return &copy, nil
}
func pluginTaskKeyValid(key string) bool {
	return strings.TrimSpace(key) != "" && len(key) <= 256 && !strings.ContainsAny(key, "\x00\r\n")
}
