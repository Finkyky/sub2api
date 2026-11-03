// Exact operation contracts mirrored from admin_route_permission.go. Coverage tests detect drift.
import type { AccessPolicy } from './policy'

export const adminOperations: Record<string, AccessPolicy> = {
  "DELETE /api/v1/admin/accounts/:id": {
    "permission": "accounts.delete"
  },
  "DELETE /api/v1/admin/accounts/:id/ollama-cloud-usage/session": {
    "permission": "accounts.update"
  },
  "DELETE /api/v1/admin/accounts/:id/temp-unschedulable": {
    "permission": "accounts.update"
  },
  "DELETE /api/v1/admin/affiliates/users/:user_id": {
    "permission": "affiliates.manage"
  },
  "DELETE /api/v1/admin/announcements/:id": {
    "permission": "announcements.delete"
  },
  "DELETE /api/v1/admin/backups/:id": {
    "superOnly": true
  },
  "DELETE /api/v1/admin/channel-monitor-templates/:id": {
    "permission": "channels.delete"
  },
  "DELETE /api/v1/admin/channel-monitors/:id": {
    "permission": "channels.delete"
  },
  "DELETE /api/v1/admin/channels/:id": {
    "permission": "channels.delete"
  },
  "DELETE /api/v1/admin/data-management/s3/profiles/:profile_id": {
    "superOnly": true
  },
  "DELETE /api/v1/admin/data-management/sources/:source_type/profiles/:profile_id": {
    "superOnly": true
  },
  "DELETE /api/v1/admin/error-passthrough-rules/:id": {
    "permission": "settings.update"
  },
  "DELETE /api/v1/admin/groups/:id": {
    "permission": "groups.delete"
  },
  "DELETE /api/v1/admin/groups/:id/composite-routes/:route_id": {
    "permission": "groups.update"
  },
  "DELETE /api/v1/admin/groups/:id/rate-multipliers": {
    "permission": "groups.update"
  },
  "DELETE /api/v1/admin/groups/:id/rpm-overrides": {
    "permission": "groups.update"
  },
  "DELETE /api/v1/admin/ops/alert-rules/:id": {
    "permission": "ops.manage"
  },
  "DELETE /api/v1/admin/payment/plans/:id": {
    "permission": "payments.configure"
  },
  "DELETE /api/v1/admin/payment/providers/:id": {
    "permission": "payments.configure"
  },
  "DELETE /api/v1/admin/plugins/:id": {
    "superOnly": true
  },
  "DELETE /api/v1/admin/promo-codes/:id": {
    "permission": "promo_codes.delete"
  },
  "DELETE /api/v1/admin/prompt-audit/events/:id": {
    "permission": "prompt_audit.delete"
  },
  "DELETE /api/v1/admin/proxies/:id": {
    "permission": "proxies.delete"
  },
  "DELETE /api/v1/admin/redeem-codes/:id": {
    "permission": "redeem_codes.delete"
  },
  "DELETE /api/v1/admin/risk-control/hashes": {
    "permission": "risk_control.manage"
  },
  "DELETE /api/v1/admin/risk-control/hashes/all": {
    "permission": "risk_control.manage"
  },
  "DELETE /api/v1/admin/roles/:id": {
    "permission": "roles.delete"
  },
  "DELETE /api/v1/admin/scheduled-test-plans/:id": {
    "permission": "accounts.update"
  },
  "DELETE /api/v1/admin/settings/admin-api-key": {
    "superOnly": true
  },
  "DELETE /api/v1/admin/subscriptions/:id": {
    "permission": "subscriptions.update"
  },
  "DELETE /api/v1/admin/tls-fingerprint-profiles/:id": {
    "permission": "settings.update"
  },
  "DELETE /api/v1/admin/user-attributes/:id": {
    "permission": "user_attributes.manage"
  },
  "DELETE /api/v1/admin/users/:id": {
    "permission": "users.delete"
  },
  "GET /api/v1/admin/accounts": {
    "permission": "accounts.read"
  },
  "GET /api/v1/admin/accounts/:id": {
    "permission": "accounts.read"
  },
  "GET /api/v1/admin/accounts/:id/grok-media-eligibility": {
    "permission": "accounts.read"
  },
  "GET /api/v1/admin/accounts/:id/models": {
    "permission": "accounts.read"
  },
  "GET /api/v1/admin/accounts/:id/ollama-cloud-usage": {
    "permission": "accounts.read"
  },
  "GET /api/v1/admin/accounts/:id/scheduled-test-plans": {
    "permission": "accounts.read"
  },
  "GET /api/v1/admin/accounts/:id/stats": {
    "permission": "accounts.read"
  },
  "GET /api/v1/admin/accounts/:id/temp-unschedulable": {
    "permission": "accounts.read"
  },
  "GET /api/v1/admin/accounts/:id/today-stats": {
    "permission": "accounts.read"
  },
  "GET /api/v1/admin/accounts/:id/usage": {
    "permission": "accounts.update"
  },
  "GET /api/v1/admin/accounts/:id/opencode-go-usage": {
    "permission": "accounts.read"
  },
  "PUT /api/v1/admin/accounts/:id/opencode-go-usage/auto-refresh": {
    "permission": "accounts.update"
  },
  "POST /api/v1/admin/accounts/:id/opencode-go-usage/refresh": {
    "permission": "accounts.update"
  },
  "GET /api/v1/admin/accounts/antigravity/default-model-mapping": {
    "permission": "accounts.read"
  },
  "GET /api/v1/admin/accounts/data": {
    "permission": "accounts.export"
  },
  "GET /api/v1/admin/accounts/ollama-cloud-usage/settings": {
    "permission": "accounts.read"
  },
  "GET /api/v1/admin/accounts/opencode-go-usage/settings": {
    "permission": "accounts.read"
  },
  "GET /api/v1/admin/accounts/upstream-billing-probe/settings": {
    "permission": "accounts.read"
  },
  "GET /api/v1/admin/accounts/upstream-billing-rates": {
    "permission": "accounts.read"
  },
  "GET /api/v1/admin/affiliates/invites": {
    "permission": "affiliates.read"
  },
  "GET /api/v1/admin/affiliates/rebates": {
    "permission": "affiliates.read"
  },
  "GET /api/v1/admin/affiliates/transfers": {
    "permission": "affiliates.read"
  },
  "GET /api/v1/admin/affiliates/users": {
    "permission": "affiliates.read"
  },
  "GET /api/v1/admin/affiliates/users/:user_id/overview": {
    "permission": "affiliates.read"
  },
  "GET /api/v1/admin/affiliates/users/lookup": {
    "permission": "affiliates.read"
  },
  "GET /api/v1/admin/announcements": {
    "permission": "announcements.read"
  },
  "GET /api/v1/admin/announcements/:id": {
    "permission": "announcements.read"
  },
  "GET /api/v1/admin/announcements/:id/read-status": {
    "permission": "announcements.read"
  },
  "GET /api/v1/admin/audit-logs": {
    "permission": "audit_logs.read"
  },
  "GET /api/v1/admin/audit-logs/:id": {
    "permission": "audit_logs.read"
  },
  "GET /api/v1/admin/backups": {
    "permission": "backups.read"
  },
  "GET /api/v1/admin/backups/:id": {
    "permission": "backups.read"
  },
  "GET /api/v1/admin/backups/:id/download-url": {
    "superOnly": true
  },
  "GET /api/v1/admin/backups/image-storage": {
    "superOnly": true
  },
  "GET /api/v1/admin/backups/s3-config": {
    "superOnly": true
  },
  "GET /api/v1/admin/backups/schedule": {
    "permission": "backups.read"
  },
  "GET /api/v1/admin/channel-monitor-templates": {
    "permission": "channels.read"
  },
  "GET /api/v1/admin/channel-monitor-templates/:id": {
    "permission": "channels.read"
  },
  "GET /api/v1/admin/channel-monitor-templates/:id/monitors": {
    "permission": "channels.read"
  },
  "GET /api/v1/admin/channel-monitor-v2/config": {
    "permission": "channels.read"
  },
  "GET /api/v1/admin/channel-monitor-v2/dimensions": {
    "permission": "channels.read"
  },
  "GET /api/v1/admin/channel-monitor-v2/errors": {
    "permission": "channels.read"
  },
  "GET /api/v1/admin/channel-monitor-v2/matrix": {
    "permission": "channels.read"
  },
  "GET /api/v1/admin/channel-monitor-v2/models": {
    "permission": "channels.read"
  },
  "GET /api/v1/admin/channel-monitor-v2/snapshot": {
    "permission": "channels.read"
  },
  "GET /api/v1/admin/channel-monitor-v2/users": {
    "permission": "channels.read"
  },
  "GET /api/v1/admin/channel-monitors": {
    "permission": "channels.read"
  },
  "GET /api/v1/admin/channel-monitors/:id": {
    "permission": "channels.read"
  },
  "GET /api/v1/admin/channel-monitors/:id/history": {
    "permission": "channels.read"
  },
  "GET /api/v1/admin/channels": {
    "permission": "channels.read"
  },
  "GET /api/v1/admin/channels/:id": {
    "permission": "channels.read"
  },
  "GET /api/v1/admin/channels/model-pricing": {
    "permission": "channels.read"
  },
  "GET /api/v1/admin/channels/pricing/sync-models": {
    "permission": "channels.read"
  },
  "GET /api/v1/admin/cn-providers/accounts/:id/balance": {
    "permission": "accounts.read"
  },
  "GET /api/v1/admin/cn-providers/accounts/:id/quota": {
    "permission": "accounts.read"
  },
  "GET /api/v1/admin/compliance": {
    "permission": "compliance.read"
  },
  "GET /api/v1/admin/dashboard/api-keys-trend": {
    "permission": "dashboard.read"
  },
  "GET /api/v1/admin/dashboard/groups": {
    "permission": "dashboard.read"
  },
  "GET /api/v1/admin/dashboard/models": {
    "permission": "dashboard.read"
  },
  "GET /api/v1/admin/dashboard/realtime": {
    "permission": "dashboard.read"
  },
  "GET /api/v1/admin/dashboard/snapshot-v2": {
    "permission": "dashboard.read"
  },
  "GET /api/v1/admin/dashboard/stats": {
    "permission": "dashboard.read"
  },
  "GET /api/v1/admin/dashboard/trend": {
    "permission": "dashboard.read"
  },
  "GET /api/v1/admin/dashboard/user-breakdown": {
    "permission": "dashboard.read"
  },
  "GET /api/v1/admin/dashboard/users-ranking": {
    "permission": "dashboard.read"
  },
  "GET /api/v1/admin/dashboard/users-trend": {
    "permission": "dashboard.read"
  },
  "GET /api/v1/admin/data-management/agent/health": {
    "superOnly": true
  },
  "GET /api/v1/admin/data-management/backups": {
    "superOnly": true
  },
  "GET /api/v1/admin/data-management/backups/:job_id": {
    "superOnly": true
  },
  "GET /api/v1/admin/data-management/config": {
    "superOnly": true
  },
  "GET /api/v1/admin/data-management/s3/profiles": {
    "superOnly": true
  },
  "GET /api/v1/admin/data-management/sources/:source_type/profiles": {
    "superOnly": true
  },
  "GET /api/v1/admin/error-passthrough-rules": {
    "permission": "settings.read"
  },
  "GET /api/v1/admin/error-passthrough-rules/:id": {
    "permission": "settings.read"
  },
  "GET /api/v1/admin/gemini/oauth/capabilities": {
    "permission": "accounts.read"
  },
  "GET /api/v1/admin/grok/accounts/:id/quota": {
    "permission": "accounts.read"
  },
  "GET /api/v1/admin/grok/oauth/capabilities": {
    "permission": "accounts.read"
  },
  "GET /api/v1/admin/grok/runtime-sanity": {
    "permission": "accounts.read"
  },
  "GET /api/v1/admin/groups": {
    "permission": "groups.read"
  },
  "GET /api/v1/admin/groups/:id": {
    "permission": "groups.read"
  },
  "GET /api/v1/admin/groups/:id/api-keys": {
    "permission": "api_keys.read"
  },
  "GET /api/v1/admin/groups/:id/composite-routes": {
    "permission": "groups.read"
  },
  "GET /api/v1/admin/groups/:id/model-allowlist-candidates": {
    "permission": "groups.read"
  },
  "GET /api/v1/admin/groups/:id/rate-multipliers": {
    "permission": "groups.read"
  },
  "GET /api/v1/admin/groups/:id/stats": {
    "permission": "groups.read"
  },
  "GET /api/v1/admin/groups/:id/subscriptions": {
    "permission": "subscriptions.read"
  },
  "GET /api/v1/admin/groups/all": {
    "permission": "groups.read"
  },
  "GET /api/v1/admin/groups/capacity-summary": {
    "permission": "groups.read"
  },
  "GET /api/v1/admin/groups/live-capability": {
    "permission": "groups.read"
  },
  "GET /api/v1/admin/groups/usage-summary": {
    "permission": "groups.read"
  },
  "GET /api/v1/admin/openai/accounts/:id/quota": {
    "permission": "accounts.read"
  },
  "GET /api/v1/admin/ops/account-availability": {
    "permission": "ops.read"
  },
  "GET /api/v1/admin/ops/advanced-settings": {
    "permission": "ops.read"
  },
  "GET /api/v1/admin/ops/alert-events": {
    "permission": "ops.read"
  },
  "GET /api/v1/admin/ops/alert-events/:id": {
    "permission": "ops.read"
  },
  "GET /api/v1/admin/ops/alert-rules": {
    "permission": "ops.read"
  },
  "GET /api/v1/admin/ops/auth-cache-invalidation/health": {
    "permission": "ops.read"
  },
  "GET /api/v1/admin/ops/concurrency": {
    "permission": "ops.read"
  },
  "GET /api/v1/admin/ops/dashboard/error-distribution": {
    "permission": "ops.read"
  },
  "GET /api/v1/admin/ops/dashboard/error-trend": {
    "permission": "ops.read"
  },
  "GET /api/v1/admin/ops/dashboard/latency-histogram": {
    "permission": "ops.read"
  },
  "GET /api/v1/admin/ops/dashboard/openai-token-stats": {
    "permission": "ops.read"
  },
  "GET /api/v1/admin/ops/dashboard/overview": {
    "permission": "ops.read"
  },
  "GET /api/v1/admin/ops/dashboard/snapshot-v2": {
    "permission": "ops.read"
  },
  "GET /api/v1/admin/ops/dashboard/throughput-trend": {
    "permission": "ops.read"
  },
  "GET /api/v1/admin/ops/email-notification/config": {
    "permission": "ops.read"
  },
  "GET /api/v1/admin/ops/errors": {
    "permission": "ops.read"
  },
  "GET /api/v1/admin/ops/errors/:id": {
    "permission": "ops.read"
  },
  "GET /api/v1/admin/ops/ingress-rejections": {
    "permission": "ops.read"
  },
  "GET /api/v1/admin/ops/ingress-rejections/health": {
    "permission": "ops.read"
  },
  "GET /api/v1/admin/ops/realtime-traffic": {
    "permission": "ops.read"
  },
  "GET /api/v1/admin/ops/request-errors": {
    "permission": "ops.read"
  },
  "GET /api/v1/admin/ops/request-errors/:id": {
    "permission": "ops.read"
  },
  "GET /api/v1/admin/ops/request-errors/:id/upstream-errors": {
    "permission": "ops.read"
  },
  "GET /api/v1/admin/ops/requests": {
    "permission": "ops.read"
  },
  "GET /api/v1/admin/ops/runtime/alert": {
    "permission": "ops.read"
  },
  "GET /api/v1/admin/ops/runtime/logging": {
    "permission": "ops.read"
  },
  "GET /api/v1/admin/ops/settings/metric-thresholds": {
    "permission": "ops.read"
  },
  "GET /api/v1/admin/ops/system-logs": {
    "permission": "ops.read"
  },
  "GET /api/v1/admin/ops/system-logs/health": {
    "permission": "ops.read"
  },
  "GET /api/v1/admin/ops/upstream-errors": {
    "permission": "ops.read"
  },
  "GET /api/v1/admin/ops/upstream-errors/:id": {
    "permission": "ops.read"
  },
  "GET /api/v1/admin/ops/user-concurrency": {
    "permission": "ops.read"
  },
  "GET /api/v1/admin/ops/ws/qps": {
    "permission": "ops.read"
  },
  "GET /api/v1/admin/payment/config": {
    "permission": "payments.read"
  },
  "GET /api/v1/admin/payment/dashboard": {
    "permission": "payments.read"
  },
  "GET /api/v1/admin/payment/orders": {
    "permission": "payments.read"
  },
  "GET /api/v1/admin/payment/orders/:id": {
    "permission": "payments.read"
  },
  "GET /api/v1/admin/payment/plans": {
    "permission": "payments.read"
  },
  "GET /api/v1/admin/payment/providers": {
    "permission": "payments.read"
  },
  "GET /api/v1/admin/permissions": {
    "permission": "permissions.read"
  },
  "GET /api/v1/admin/plugins": {
    "permission": "plugins.read"
  },
  "GET /api/v1/admin/plugins/:id": {
    "permission": "plugins.read"
  },
  "GET /api/v1/admin/plugins/:id/config": {
    "superOnly": true
  },
  "GET /api/v1/admin/plugins/:id/status": {
    "permission": "plugins.read"
  },
  "GET /api/v1/admin/plugin-tasks": {
    "permission": "plugin_tasks.read"
  },
  "GET /api/v1/admin/plugin-tasks/:id": {
    "permission": "plugin_tasks.read"
  },
  "POST /api/v1/admin/plugin-tasks": {
    "permission": "plugin_tasks.create"
  },
  "PUT /api/v1/admin/plugin-tasks/:id": {
    "permission": "plugin_tasks.update"
  },
  "DELETE /api/v1/admin/plugin-tasks/:id": {
    "permission": "plugin_tasks.delete"
  },
  "POST /api/v1/admin/plugin-tasks/:id/run": {
    "permission": "plugin_tasks.run"
  },
  "GET /api/v1/admin/plugin-tasks/:id/runs": {
    "permission": "plugin_runs.read"
  },
  "GET /api/v1/admin/plugin-runs/:id": {
    "permission": "plugin_runs.read"
  },
  "GET /api/v1/admin/plugin-runs/:id/sensitive": {
    "permission": "plugin_runs.sensitive"
  },
  "GET /api/v1/admin/plugin-instances": {
    "permission": "plugin_instances.read"
  },
  "GET /api/v1/admin/plugin-instances/:id": {
    "permission": "plugin_instances.read"
  },
  "GET /api/v1/admin/plugin-instances/:id/revisions": {
    "permission": "plugin_instances.read"
  },
  "GET /api/v1/admin/plugin-instances/:id/status": {
    "permission": "plugin_instances.read"
  },
  "POST /api/v1/admin/plugin-instances": {
    "permission": "plugin_instances.create"
  },
  "PUT /api/v1/admin/plugin-instances/:id/config": {
    "permission": "plugin_instances.update"
  },
  "POST /api/v1/admin/plugin-instances/:id/publish": {
    "permission": "plugin_instances.publish"
  },
  "DELETE /api/v1/admin/plugin-instances/:id": {
    "permission": "plugin_instances.delete"
  },
  "POST /api/v1/admin/plugin-instances/:id/disable": {
    "permission": "plugin_instances.disable"
  },
  "PUT /api/v1/admin/plugin-instances/:id/bindings": {
    "permission": "plugin_instances.bindings.update"
  },
  "GET /api/v1/admin/promo-codes": {
    "permission": "promo_codes.read"
  },
  "GET /api/v1/admin/promo-codes/:id": {
    "permission": "promo_codes.read"
  },
  "GET /api/v1/admin/promo-codes/:id/usages": {
    "permission": "promo_codes.read"
  },
  "GET /api/v1/admin/prompt-audit/config": {
    "permission": "prompt_audit.read"
  },
  "GET /api/v1/admin/prompt-audit/events": {
    "permission": "prompt_audit.read"
  },
  "GET /api/v1/admin/prompt-audit/events/:id": {
    "permission": "prompt_audit.read"
  },
  "GET /api/v1/admin/prompt-audit/runtime": {
    "permission": "prompt_audit.read"
  },
  "GET /api/v1/admin/proxies": {
    "permission": "proxies.read"
  },
  "GET /api/v1/admin/proxies/:id": {
    "permission": "proxies.read"
  },
  "GET /api/v1/admin/proxies/:id/accounts": {
    "permission": "accounts.read"
  },
  "GET /api/v1/admin/proxies/:id/stats": {
    "permission": "proxies.read"
  },
  "GET /api/v1/admin/proxies/all": {
    "permission": "proxies.read"
  },
  "GET /api/v1/admin/proxies/data": {
    "permission": "proxies.export"
  },
  "GET /api/v1/admin/redeem-codes": {
    "permission": "redeem_codes.read"
  },
  "GET /api/v1/admin/redeem-codes/:id": {
    "permission": "redeem_codes.read"
  },
  "GET /api/v1/admin/redeem-codes/export": {
    "permission": "redeem_codes.export"
  },
  "GET /api/v1/admin/redeem-codes/stats": {
    "permission": "redeem_codes.read"
  },
  "GET /api/v1/admin/risk-control/config": {
    "permission": "risk_control.read"
  },
  "GET /api/v1/admin/risk-control/logs": {
    "permission": "risk_control.read"
  },
  "GET /api/v1/admin/risk-control/status": {
    "permission": "risk_control.read"
  },
  "GET /api/v1/admin/roles": {
    "permission": "roles.read"
  },
  "GET /api/v1/admin/roles/:id": {
    "permission": "roles.read"
  },
  "GET /api/v1/admin/roles/:id/permissions": {
    "permission": "roles.read"
  },
  "GET /api/v1/admin/scheduled-test-plans/:id/results": {
    "permission": "accounts.read"
  },
  "GET /api/v1/admin/settings": {
    "permission": "settings.read"
  },
  "GET /api/v1/admin/settings/admin-api-key": {
    "superOnly": true
  },
  "GET /api/v1/admin/settings/beta-policy": {
    "permission": "settings.read"
  },
  "GET /api/v1/admin/settings/email-templates": {
    "permission": "settings.read"
  },
  "GET /api/v1/admin/settings/email-templates/:event/:locale": {
    "permission": "settings.read"
  },
  "GET /api/v1/admin/settings/openai-images-oauth-unavailable-cooldown": {
    "permission": "settings.read"
  },
  "GET /api/v1/admin/settings/overload-cooldown": {
    "permission": "settings.read"
  },
  "GET /api/v1/admin/settings/panel-rate-limit": {
    "permission": "settings.read"
  },
  "GET /api/v1/admin/settings/rate-limit-429-cooldown": {
    "permission": "settings.read"
  },
  "GET /api/v1/admin/settings/rectifier": {
    "permission": "settings.read"
  },
  "GET /api/v1/admin/settings/stream-timeout": {
    "permission": "settings.read"
  },
  "GET /api/v1/admin/settings/web-search-emulation": {
    "permission": "settings.read"
  },
  "GET /api/v1/admin/subscriptions": {
    "permission": "subscriptions.read"
  },
  "GET /api/v1/admin/subscriptions/:id": {
    "permission": "subscriptions.read"
  },
  "GET /api/v1/admin/subscriptions/:id/progress": {
    "permission": "subscriptions.read"
  },
  "GET /api/v1/admin/system/check-updates": {
    "permission": "system.read"
  },
  "GET /api/v1/admin/system/rollback-versions": {
    "permission": "system.read"
  },
  "GET /api/v1/admin/system/version": {
    "permission": "system.read"
  },
  "GET /api/v1/admin/tls-fingerprint-profiles": {
    "permission": "settings.read"
  },
  "GET /api/v1/admin/tls-fingerprint-profiles/:id": {
    "permission": "settings.read"
  },
  "GET /api/v1/admin/usage": {
    "permission": "usage.read"
  },
  "GET /api/v1/admin/usage/cleanup-tasks": {
    "permission": "usage.read"
  },
  "GET /api/v1/admin/usage/search-api-keys": {
    "permission": "usage.read"
  },
  "GET /api/v1/admin/usage/search-users": {
    "permission": "usage.read"
  },
  "GET /api/v1/admin/usage/stats": {
    "permission": "usage.read"
  },
  "GET /api/v1/admin/user-attributes": {
    "permission": "user_attributes.read"
  },
  "GET /api/v1/admin/users": {
    "permission": "users.read"
  },
  "GET /api/v1/admin/users/:id": {
    "permission": "users.read"
  },
  "GET /api/v1/admin/users/:id/api-keys": {
    "permission": "api_keys.read"
  },
  "GET /api/v1/admin/users/:id/attributes": {
    "permission": "users.read"
  },
  "GET /api/v1/admin/users/:id/balance-history": {
    "permission": "users.read"
  },
  "GET /api/v1/admin/users/:id/platform-quotas": {
    "permission": "users.read"
  },
  "GET /api/v1/admin/users/:id/roles": {
    "permission": "users.read"
  },
  "GET /api/v1/admin/users/:id/rpm-status": {
    "permission": "users.read"
  },
  "GET /api/v1/admin/users/:id/subscriptions": {
    "permission": "users.read"
  },
  "GET /api/v1/admin/users/:id/usage": {
    "permission": "users.read"
  },
  "POST /api/v1/admin/accounts": {
    "permission": "accounts.create"
  },
  "POST /api/v1/admin/accounts/:id/apply-oauth-credentials": {
    "permission": "accounts.update"
  },
  "POST /api/v1/admin/accounts/:id/clear-error": {
    "permission": "accounts.update"
  },
  "POST /api/v1/admin/accounts/:id/clear-rate-limit": {
    "permission": "accounts.update"
  },
  "POST /api/v1/admin/accounts/:id/duplicate": {
    "permission": "accounts.create"
  },
  "POST /api/v1/admin/accounts/:id/models/sync-upstream": {
    "permission": "accounts.update"
  },
  "POST /api/v1/admin/accounts/:id/ollama-cloud-usage/refresh": {
    "permission": "accounts.update"
  },
  "POST /api/v1/admin/accounts/:id/recover-state": {
    "permission": "accounts.update"
  },
  "POST /api/v1/admin/accounts/:id/refresh": {
    "permission": "accounts.update"
  },
  "POST /api/v1/admin/accounts/:id/refresh-tier": {
    "permission": "accounts.update"
  },
  "POST /api/v1/admin/accounts/:id/reset-quota": {
    "permission": "accounts.update"
  },
  "POST /api/v1/admin/accounts/:id/revert-proxy-fallback": {
    "permission": "accounts.update"
  },
  "POST /api/v1/admin/accounts/:id/schedulable": {
    "permission": "accounts.update"
  },
  "POST /api/v1/admin/accounts/:id/set-privacy": {
    "permission": "accounts.update"
  },
  "POST /api/v1/admin/accounts/:id/shadow": {
    "permission": "accounts.create"
  },
  "POST /api/v1/admin/accounts/:id/test": {
    "permission": "accounts.update"
  },
  "POST /api/v1/admin/accounts/:id/upstream-billing-probe": {
    "permission": "accounts.update"
  },
  "POST /api/v1/admin/accounts/batch": {
    "permission": "accounts.create"
  },
  "POST /api/v1/admin/accounts/batch-clear-error": {
    "permission": "accounts.update"
  },
  "POST /api/v1/admin/accounts/batch-delete": {
    "permission": "accounts.delete"
  },
  "POST /api/v1/admin/accounts/batch-refresh": {
    "permission": "accounts.update"
  },
  "POST /api/v1/admin/accounts/batch-refresh-tier": {
    "permission": "accounts.update"
  },
  "POST /api/v1/admin/accounts/batch-update-credentials": {
    "permission": "accounts.update"
  },
  "POST /api/v1/admin/accounts/bulk-update": {
    "permission": "accounts.update"
  },
  "POST /api/v1/admin/accounts/check-mixed-channel": {
    "permission": "accounts.read"
  },
  "POST /api/v1/admin/accounts/cookie-auth": {
    "permission": "accounts.create"
  },
  "POST /api/v1/admin/accounts/data": {
    "permission": "accounts.import"
  },
  "POST /api/v1/admin/accounts/exchange-code": {
    "permission": "accounts.create"
  },
  "POST /api/v1/admin/accounts/exchange-setup-token-code": {
    "permission": "accounts.create"
  },
  "POST /api/v1/admin/accounts/generate-auth-url": {
    "permission": "accounts.create"
  },
  "POST /api/v1/admin/accounts/generate-setup-token-url": {
    "permission": "accounts.create"
  },
  "POST /api/v1/admin/accounts/import/codex-session": {
    "permission": "accounts.import"
  },
  "POST /api/v1/admin/accounts/models/sync-upstream-preview": {
    "permission": "accounts.update"
  },
  "POST /api/v1/admin/accounts/setup-token-cookie-auth": {
    "permission": "accounts.create"
  },
  "POST /api/v1/admin/accounts/sync/crs": {
    "permission": "accounts.import"
  },
  "POST /api/v1/admin/accounts/sync/crs/preview": {
    "permission": "accounts.update"
  },
  "POST /api/v1/admin/accounts/today-stats/batch": {
    "permission": "accounts.read"
  },
  "POST /api/v1/admin/accounts/upstream-billing-probe/batch": {
    "permission": "accounts.update"
  },
  "POST /api/v1/admin/accounts/usage/batch": {
    "permission": "accounts.update"
  },
  "POST /api/v1/admin/affiliates/users/batch-rate": {
    "permission": "affiliates.manage"
  },
  "POST /api/v1/admin/announcements": {
    "permission": "announcements.create"
  },
  "POST /api/v1/admin/antigravity/oauth/auth-url": {
    "permission": "accounts.create"
  },
  "POST /api/v1/admin/antigravity/oauth/exchange-code": {
    "permission": "accounts.create"
  },
  "POST /api/v1/admin/antigravity/oauth/refresh-token": {
    "permission": "accounts.update"
  },
  "POST /api/v1/admin/audit-logs/clear": {
    "permission": "audit_logs.delete"
  },
  "POST /api/v1/admin/backups": {
    "superOnly": true
  },
  "POST /api/v1/admin/backups/:id/restore": {
    "superOnly": true
  },
  "POST /api/v1/admin/backups/image-storage/test": {
    "superOnly": true
  },
  "POST /api/v1/admin/backups/s3-config/test": {
    "superOnly": true
  },
  "POST /api/v1/admin/channel-monitor-templates": {
    "permission": "channels.create"
  },
  "POST /api/v1/admin/channel-monitor-templates/:id/apply": {
    "permission": "channels.update"
  },
  "POST /api/v1/admin/channel-monitors": {
    "permission": "channels.create"
  },
  "POST /api/v1/admin/channel-monitors/:id/duplicate": {
    "permission": "channels.create"
  },
  "POST /api/v1/admin/channel-monitors/:id/run": {
    "permission": "channels.update"
  },
  "POST /api/v1/admin/channels": {
    "permission": "channels.create"
  },
  "POST /api/v1/admin/compliance/accept": {
    "permission": "compliance.accept"
  },
  "POST /api/v1/admin/dashboard/aggregation/backfill": {
    "permission": "ops.manage"
  },
  "POST /api/v1/admin/dashboard/api-keys-usage": {
    "permission": "dashboard.read"
  },
  "POST /api/v1/admin/dashboard/users-usage": {
    "permission": "dashboard.read"
  },
  "POST /api/v1/admin/data-management/backups": {
    "superOnly": true
  },
  "POST /api/v1/admin/data-management/s3/profiles": {
    "superOnly": true
  },
  "POST /api/v1/admin/data-management/s3/profiles/:profile_id/activate": {
    "superOnly": true
  },
  "POST /api/v1/admin/data-management/s3/test": {
    "superOnly": true
  },
  "POST /api/v1/admin/data-management/sources/:source_type/profiles": {
    "superOnly": true
  },
  "POST /api/v1/admin/data-management/sources/:source_type/profiles/:profile_id/activate": {
    "superOnly": true
  },
  "POST /api/v1/admin/error-passthrough-rules": {
    "permission": "settings.update"
  },
  "POST /api/v1/admin/gemini/oauth/auth-url": {
    "permission": "accounts.create"
  },
  "POST /api/v1/admin/gemini/oauth/exchange-code": {
    "permission": "accounts.create"
  },
  "POST /api/v1/admin/grok/accounts/:id/refresh": {
    "permission": "accounts.update"
  },
  "POST /api/v1/admin/grok/accounts/:id/reset-quota": {
    "permission": "accounts.update"
  },
  "POST /api/v1/admin/grok/oauth/auth-url": {
    "permission": "accounts.create"
  },
  "POST /api/v1/admin/grok/oauth/create-from-oauth": {
    "permission": "accounts.create"
  },
  "POST /api/v1/admin/grok/oauth/exchange-code": {
    "permission": "accounts.create"
  },
  "POST /api/v1/admin/grok/oauth/password": {
    "permission": "accounts.create"
  },
  "POST /api/v1/admin/grok/oauth/reconcile": {
    "permission": "accounts.update"
  },
  "POST /api/v1/admin/grok/oauth/refresh-token": {
    "permission": "accounts.update"
  },
  "POST /api/v1/admin/grok/oauth/sso-token": {
    "permission": "accounts.create"
  },
  "POST /api/v1/admin/grok/sso-to-oauth": {
    "permission": "accounts.create"
  },
  "POST /api/v1/admin/groups": {
    "permission": "groups.create"
  },
  "POST /api/v1/admin/groups/:id/composite-routes": {
    "permission": "groups.update"
  },
  "POST /api/v1/admin/groups/:id/composite-routes/preview": {
    "permission": "groups.read"
  },
  "POST /api/v1/admin/groups/:id/duplicate": {
    "permission": "groups.create"
  },
  "POST /api/v1/admin/openai/accounts/:id/quota/refresh": {
    "permission": "accounts.update"
  },
  "POST /api/v1/admin/openai/accounts/:id/refresh": {
    "permission": "accounts.update"
  },
  "POST /api/v1/admin/openai/accounts/:id/reset-quota": {
    "permission": "accounts.update"
  },
  "POST /api/v1/admin/openai/create-from-codex-pat": {
    "permission": "accounts.create"
  },
  "POST /api/v1/admin/openai/create-from-oauth": {
    "permission": "accounts.create"
  },
  "POST /api/v1/admin/openai/exchange-code": {
    "permission": "accounts.create"
  },
  "POST /api/v1/admin/openai/generate-auth-url": {
    "permission": "accounts.create"
  },
  "POST /api/v1/admin/openai/refresh-token": {
    "permission": "accounts.update"
  },
  "POST /api/v1/admin/ops/alert-rules": {
    "permission": "ops.manage"
  },
  "POST /api/v1/admin/ops/alert-silences": {
    "permission": "ops.manage"
  },
  "POST /api/v1/admin/ops/runtime/logging/reset": {
    "permission": "ops.manage"
  },
  "POST /api/v1/admin/ops/system-logs/cleanup": {
    "permission": "ops.manage"
  },
  "POST /api/v1/admin/payment/orders/:id/cancel": {
    "permission": "payments.update"
  },
  "POST /api/v1/admin/payment/orders/:id/refund": {
    "permission": "payments.refund"
  },
  "POST /api/v1/admin/payment/orders/:id/refund/query": {
    "permission": "payments.refund"
  },
  "POST /api/v1/admin/payment/orders/:id/retry": {
    "permission": "payments.update"
  },
  "POST /api/v1/admin/payment/plans": {
    "permission": "payments.configure"
  },
  "POST /api/v1/admin/payment/providers": {
    "permission": "payments.configure"
  },
  "POST /api/v1/admin/plugins/:id/disable": {
    "superOnly": true
  },
  "POST /api/v1/admin/plugins/:id/enable": {
    "superOnly": true
  },
  "POST /api/v1/admin/plugins/:id/test": {
    "superOnly": true
  },
  "POST /api/v1/admin/plugins/:id/ui-session": {
    "superOnly": true
  },
  "POST /api/v1/admin/plugins/upload": {
    "superOnly": true
  },
  "POST /api/v1/admin/promo-codes": {
    "permission": "promo_codes.create"
  },
  "POST /api/v1/admin/prompt-audit/endpoints/probe": {
    "permission": "prompt_audit.manage"
  },
  "POST /api/v1/admin/prompt-audit/events/batch-delete": {
    "permission": "prompt_audit.delete"
  },
  "POST /api/v1/admin/prompt-audit/events/delete-by-filter": {
    "permission": "prompt_audit.delete"
  },
  "POST /api/v1/admin/prompt-audit/events/delete-preview": {
    "permission": "prompt_audit.read"
  },
  "POST /api/v1/admin/proxies": {
    "permission": "proxies.create"
  },
  "POST /api/v1/admin/proxies/:id/quality-check": {
    "permission": "proxies.update"
  },
  "POST /api/v1/admin/proxies/:id/test": {
    "permission": "proxies.update"
  },
  "POST /api/v1/admin/proxies/batch": {
    "permission": "proxies.create"
  },
  "POST /api/v1/admin/proxies/batch-delete": {
    "permission": "proxies.delete"
  },
  "POST /api/v1/admin/proxies/data": {
    "permission": "proxies.import"
  },
  "POST /api/v1/admin/redeem-codes/:id/expire": {
    "permission": "redeem_codes.update"
  },
  "POST /api/v1/admin/redeem-codes/batch-delete": {
    "permission": "redeem_codes.delete"
  },
  "POST /api/v1/admin/redeem-codes/batch-update": {
    "permission": "redeem_codes.update"
  },
  "POST /api/v1/admin/redeem-codes/create-and-redeem": {
    "permission": "redeem_codes.create"
  },
  "POST /api/v1/admin/redeem-codes/generate": {
    "permission": "redeem_codes.create"
  },
  "POST /api/v1/admin/risk-control/api-keys/test": {
    "permission": "risk_control.manage"
  },
  "POST /api/v1/admin/risk-control/users/:user_id/unban": {
    "permission": "risk_control.manage"
  },
  "POST /api/v1/admin/roles": {
    "permission": "roles.create"
  },
  "POST /api/v1/admin/scheduled-test-plans": {
    "permission": "accounts.update"
  },
  "POST /api/v1/admin/settings/admin-api-key/regenerate": {
    "superOnly": true
  },
  "POST /api/v1/admin/settings/email-template-preview": {
    "permission": "settings.read"
  },
  "POST /api/v1/admin/settings/email-templates/:event/:locale/restore-official": {
    "permission": "settings.update"
  },
  "POST /api/v1/admin/settings/send-test-email": {
    "permission": "settings.update"
  },
  "POST /api/v1/admin/settings/test-smtp": {
    "permission": "settings.update"
  },
  "POST /api/v1/admin/settings/web-search-emulation/reset-usage": {
    "permission": "settings.update"
  },
  "POST /api/v1/admin/settings/web-search-emulation/test": {
    "permission": "settings.update"
  },
  "POST /api/v1/admin/subscriptions/:id/extend": {
    "permission": "subscriptions.update"
  },
  "POST /api/v1/admin/subscriptions/:id/reset-quota": {
    "permission": "subscriptions.update"
  },
  "POST /api/v1/admin/subscriptions/:id/restore": {
    "permission": "subscriptions.update"
  },
  "POST /api/v1/admin/subscriptions/:id/revoke": {
    "permission": "subscriptions.update"
  },
  "POST /api/v1/admin/subscriptions/assign": {
    "permission": "subscriptions.update"
  },
  "POST /api/v1/admin/subscriptions/bulk-action": {
    "permission": "subscriptions.update"
  },
  "POST /api/v1/admin/subscriptions/bulk-assign": {
    "permission": "subscriptions.update"
  },
  "POST /api/v1/admin/system/restart": {
    "superOnly": true
  },
  "POST /api/v1/admin/system/rollback": {
    "superOnly": true
  },
  "POST /api/v1/admin/system/update": {
    "superOnly": true
  },
  "POST /api/v1/admin/tls-fingerprint-profiles": {
    "permission": "settings.update"
  },
  "POST /api/v1/admin/usage/cleanup-tasks": {
    "permission": "usage.manage"
  },
  "POST /api/v1/admin/usage/cleanup-tasks/:id/cancel": {
    "permission": "usage.manage"
  },
  "POST /api/v1/admin/user-attributes": {
    "permission": "user_attributes.manage"
  },
  "POST /api/v1/admin/user-attributes/batch": {
    "permission": "user_attributes.read"
  },
  "POST /api/v1/admin/users": {
    "permission": "users.create"
  },
  "POST /api/v1/admin/users/:id/auth-identities": {
    "permission": "users.update"
  },
  "POST /api/v1/admin/users/:id/balance": {
    "permission": "users.update"
  },
  "POST /api/v1/admin/users/:id/platform-quotas/reset": {
    "permission": "users.update"
  },
  "POST /api/v1/admin/users/:id/replace-group": {
    "permission": "users.update"
  },
  "POST /api/v1/admin/users/batch-concurrency": {
    "permission": "users.update"
  },
  "POST /api/v1/admin/users/batch-limits": {
    "permission": "users.update"
  },
  "PUT /api/v1/admin/accounts/:id": {
    "permission": "accounts.update"
  },
  "PUT /api/v1/admin/accounts/:id/grok-media-eligibility": {
    "permission": "accounts.update"
  },
  "PUT /api/v1/admin/accounts/:id/ollama-cloud-usage/auto-refresh": {
    "permission": "accounts.update"
  },
  "PUT /api/v1/admin/accounts/:id/ollama-cloud-usage/session": {
    "permission": "accounts.update"
  },
  "PUT /api/v1/admin/accounts/:id/upstream-billing-probe": {
    "permission": "accounts.update"
  },
  "PUT /api/v1/admin/accounts/ollama-cloud-usage/settings": {
    "permission": "accounts.update"
  },
  "PUT /api/v1/admin/accounts/opencode-go-usage/settings": {
    "permission": "accounts.update"
  },
  "PUT /api/v1/admin/accounts/upstream-billing-probe/settings": {
    "permission": "accounts.update"
  },
  "PUT /api/v1/admin/affiliates/users/:user_id": {
    "permission": "affiliates.manage"
  },
  "PUT /api/v1/admin/announcements/:id": {
    "permission": "announcements.update"
  },
  "PUT /api/v1/admin/api-keys/:id": {
    "permission": "api_keys.update"
  },
  "PUT /api/v1/admin/backups/image-storage": {
    "superOnly": true
  },
  "PUT /api/v1/admin/backups/s3-config": {
    "superOnly": true
  },
  "PUT /api/v1/admin/backups/schedule": {
    "superOnly": true
  },
  "PUT /api/v1/admin/channel-monitor-templates/:id": {
    "permission": "channels.update"
  },
  "PUT /api/v1/admin/channel-monitor-v2/config": {
    "permission": "channels.update"
  },
  "PUT /api/v1/admin/channel-monitors/:id": {
    "permission": "channels.update"
  },
  "PUT /api/v1/admin/channels/:id": {
    "permission": "channels.update"
  },
  "PUT /api/v1/admin/data-management/config": {
    "superOnly": true
  },
  "PUT /api/v1/admin/data-management/s3/profiles/:profile_id": {
    "superOnly": true
  },
  "PUT /api/v1/admin/data-management/sources/:source_type/profiles/:profile_id": {
    "superOnly": true
  },
  "PUT /api/v1/admin/error-passthrough-rules/:id": {
    "permission": "settings.update"
  },
  "PUT /api/v1/admin/groups/:id": {
    "permission": "groups.update"
  },
  "PUT /api/v1/admin/groups/:id/composite-routes/:route_id": {
    "permission": "groups.update"
  },
  "PUT /api/v1/admin/groups/:id/rate-multipliers": {
    "permission": "groups.update"
  },
  "PUT /api/v1/admin/groups/:id/rpm-overrides": {
    "permission": "groups.update"
  },
  "PUT /api/v1/admin/groups/sort-order": {
    "permission": "groups.update"
  },
  "PUT /api/v1/admin/ops/advanced-settings": {
    "permission": "ops.manage"
  },
  "PUT /api/v1/admin/ops/alert-events/:id/status": {
    "permission": "ops.manage"
  },
  "PUT /api/v1/admin/ops/alert-rules/:id": {
    "permission": "ops.manage"
  },
  "PUT /api/v1/admin/ops/email-notification/config": {
    "permission": "ops.manage"
  },
  "PUT /api/v1/admin/ops/errors/:id/resolve": {
    "permission": "ops.manage"
  },
  "PUT /api/v1/admin/ops/request-errors/:id/resolve": {
    "permission": "ops.manage"
  },
  "PUT /api/v1/admin/ops/runtime/alert": {
    "permission": "ops.manage"
  },
  "PUT /api/v1/admin/ops/runtime/logging": {
    "permission": "ops.manage"
  },
  "PUT /api/v1/admin/ops/settings/metric-thresholds": {
    "permission": "ops.manage"
  },
  "PUT /api/v1/admin/ops/upstream-errors/:id/resolve": {
    "permission": "ops.manage"
  },
  "PUT /api/v1/admin/payment/config": {
    "permission": "payments.configure"
  },
  "PUT /api/v1/admin/payment/plans/:id": {
    "permission": "payments.configure"
  },
  "PUT /api/v1/admin/payment/providers/:id": {
    "permission": "payments.configure"
  },
  "PUT /api/v1/admin/plugins/:id/config": {
    "superOnly": true
  },
  "PUT /api/v1/admin/promo-codes/:id": {
    "permission": "promo_codes.update"
  },
  "PUT /api/v1/admin/prompt-audit/config": {
    "permission": "prompt_audit.manage"
  },
  "PUT /api/v1/admin/proxies/:id": {
    "permission": "proxies.update"
  },
  "PUT /api/v1/admin/risk-control/config": {
    "permission": "risk_control.manage"
  },
  "PUT /api/v1/admin/roles/:id": {
    "permission": "roles.update"
  },
  "PUT /api/v1/admin/roles/:id/permissions": {
    "permission": "roles.permissions.assign"
  },
  "PUT /api/v1/admin/scheduled-test-plans/:id": {
    "permission": "accounts.update"
  },
  "PUT /api/v1/admin/settings": {
    "permission": "settings.update"
  },
  "PUT /api/v1/admin/settings/beta-policy": {
    "permission": "settings.update"
  },
  "PUT /api/v1/admin/settings/email-templates/:event/:locale": {
    "permission": "settings.update"
  },
  "PUT /api/v1/admin/settings/openai-images-oauth-unavailable-cooldown": {
    "permission": "settings.update"
  },
  "PUT /api/v1/admin/settings/overload-cooldown": {
    "permission": "settings.update"
  },
  "PUT /api/v1/admin/settings/panel-rate-limit": {
    "permission": "settings.update"
  },
  "PUT /api/v1/admin/settings/rate-limit-429-cooldown": {
    "permission": "settings.update"
  },
  "PUT /api/v1/admin/settings/rectifier": {
    "permission": "settings.update"
  },
  "PUT /api/v1/admin/settings/stream-timeout": {
    "permission": "settings.update"
  },
  "PUT /api/v1/admin/settings/web-search-emulation": {
    "permission": "settings.update"
  },
  "PUT /api/v1/admin/tls-fingerprint-profiles/:id": {
    "permission": "settings.update"
  },
  "PUT /api/v1/admin/user-attributes/:id": {
    "permission": "user_attributes.manage"
  },
  "PUT /api/v1/admin/user-attributes/reorder": {
    "permission": "user_attributes.manage"
  },
  "PUT /api/v1/admin/users/:id": {
    "permission": "users.update"
  },
  "PUT /api/v1/admin/users/:id/attributes": {
    "permission": "users.update"
  },
  "PUT /api/v1/admin/users/:id/platform-quotas": {
    "permission": "users.update"
  },
  "PUT /api/v1/admin/users/:id/roles": {
    "permission": "users.roles.assign"
  },
  "GET /api/v1/admin/accounts/:id/claude/reset-credits": {
    "permission": "accounts.read"
  },
  "POST /api/v1/admin/accounts/:id/claude/reset-credits/redeem": {
    "permission": "accounts.update"
  },
  "GET /api/v1/admin/settings/admin-api-keys": {
    "permission": "settings.update"
  },
  "POST /api/v1/admin/settings/admin-api-keys": {
    "permission": "settings.update"
  },
  "DELETE /api/v1/admin/settings/admin-api-keys/:id": {
    "permission": "settings.update"
  },
  "PUT /api/v1/admin/settings/admin-api-keys/:id": {
    "permission": "settings.update"
  },
  "POST /api/v1/admin/settings/admin-api-keys/:id/rotate": {
    "permission": "settings.update"
  },
  "DELETE /api/v1/admin/settings/admin-api-keys/:id/record": {
    "permission": "settings.update"
  },
  "POST /api/v1/admin/affiliates/users/:user_id/withdraw": {
    "permission": "affiliates.manage"
  },
  "POST /api/v1/admin/openai/accounts/:id/referrals/refresh": {
    "permission": "accounts.update"
  },
  "POST /api/v1/admin/openai/accounts/:id/referrals/invite": {
    "permission": "accounts.update"
  }
}
