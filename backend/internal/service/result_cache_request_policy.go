package service

import (
	"context"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
)

// This is a request-local authoritative version pin, not a revision for the
// moderation/channel in-memory snapshots. It closes only changes between the
// start of preprocessing and Begin; pre-existing stale snapshots need their own
// authority binding. The first binding (including failure) is never replaced.
type resultCacheRequestPolicyKey struct{}
type resultCacheRequestPolicy struct {
	service  *ResultCacheService
	groupID  int64
	version  string
	reason   string
	channels *resultCacheChannelSnapshots
}

// BindRequestPolicy must run before any policy-dependent request transformation,
// moderation or channel mapping. An early authority failure disables only result
// caching; existing forwarding and audit behavior remains the caller's concern.
// Undeclared and explicit bypass requests do not incur an extra authority read.
func (s *ResultCacheService) BindRequestPolicy(ctx context.Context, intent ResultCacheIntent, groupID *int64) context.Context {
	if !intent.Declared || intent.Mode == "bypass" {
		return ctx
	}
	if _, ok := ctx.Value(resultCacheRequestPolicyKey{}).(resultCacheRequestPolicy); ok {
		return ctx
	}
	pin := resultCacheRequestPolicy{service: s}
	if groupID != nil {
		pin.groupID = *groupID
	}
	switch {
	case s == nil || s.policy == nil || s.store == nil:
		pin.reason = "unavailable"
	case s.isStopped():
		pin.reason = "service_stopped"
	case ctx.Err() != nil:
		pin.reason = "policy_error"
	default:
		p, err := s.current(ctx, pin.groupID)
		switch {
		case err != nil || p.Version == "":
			pin.reason = "policy_error"
		case !p.Active || !p.Config.Enabled:
			pin.reason = "disabled"
		default:
			pin.version = p.Version
		}
	}
	if pin.reason == "" {
		pin.channels = &resultCacheChannelSnapshots{parent: ctx, groupID: pin.groupID, version: pin.version, cells: make(map[*ChannelService]*resultCacheChannelSnapshot)}
	}
	return context.WithValue(ctx, resultCacheRequestPolicyKey{}, pin)
}

func (s *ResultCacheService) requestPolicyReason(ctx context.Context, groupID int64, version string) string {
	if denied, _ := ctx.Value(pluginGatewayCacheBypassKey{}).(bool); denied {
		return "plugin_bound"
	}
	pin, ok := ctx.Value(resultCacheRequestPolicyKey{}).(resultCacheRequestPolicy)
	if !ok {
		return ""
	} // Preserve direct Begin callers; Chat handlers always bind.
	if pin.service != s || pin.groupID != groupID {
		return "policy_changed"
	}
	if pin.reason != "" {
		return pin.reason
	}
	if pin.version != version {
		return "policy_changed"
	}
	return pin.channels.failure()
}

// CopyResultCacheUsageContext transfers only the request's authority pin and
// channel lookup selectors. The worker keeps its own cancellation/deadline;
// lazy snapshot builds still obey the original request's DB budget. Completed
// immutable cells are reused even after the HTTP request has been canceled.
// It never rebinds authority or copies unrelated request context values.
func CopyResultCacheUsageContext(base, request context.Context) context.Context {
	if base == nil {
		base = context.Background()
	}
	if request == nil {
		return base
	}
	if pin, ok := request.Value(resultCacheRequestPolicyKey{}).(resultCacheRequestPolicy); ok {
		base = context.WithValue(base, resultCacheRequestPolicyKey{}, pin)
	}
	if platform, ok := request.Value(ctxkey.ForcePlatform).(string); ok && platform != "" {
		base = context.WithValue(base, ctxkey.ForcePlatform, platform)
	}
	if platform, ok := ResolvedTargetPlatformFromContext(request); ok {
		base = WithResolvedTargetPlatform(base, platform)
	}
	return base
}
