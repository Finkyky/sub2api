/** Shared, fail-closed policies for routes, navigation and visible actions. */
export interface AccessPolicy { permission?: string; superOnly?: boolean }
export interface PermissionSubject { isSuperAdmin: boolean; hasPermission: (permission: string) => boolean }
export type PermissionBinding = string | AccessPolicy | readonly string[]
export function allows(subject: PermissionSubject, policy?: PermissionBinding): boolean {
  if (!policy) return false
  if (typeof policy === 'string') return policy === '@super' ? subject.isSuperAdmin : subject.hasPermission(policy)
  if (Array.isArray(policy)) return policy.length > 0 && policy.some(p => allows(subject, p))
  const rule = policy as AccessPolicy
  if (rule.superOnly) return subject.isSuperAdmin
  return !!rule.permission && subject.hasPermission(rule.permission)
}
export const adminRoutePolicies: Record<string, AccessPolicy> = {
  "/admin/permissions": { "permission": "permissions.read" },
  "/admin": {
    "permission": "dashboard.read"
  },
  "/admin/dashboard": {
    "permission": "dashboard.read"
  },
  "/admin/ops": {
    "permission": "ops.read"
  },
  "/admin/audit-logs": {
    "permission": "audit_logs.read"
  },
  "/admin/users": {
    "permission": "users.read"
  },
  "/admin/roles": {
    "permission": "roles.read"
  },
  "/admin/groups": {
    "permission": "groups.read"
  },
  "/admin/channels": {
    "permission": "channels.read"
  },
  "/admin/channels/pricing": {
    "permission": "channels.read"
  },
  "/admin/channels/monitor": {
    "permission": "channels.read"
  },
  "/admin/subscriptions": {
    "permission": "subscriptions.read"
  },
  "/admin/accounts": {
    "permission": "accounts.read"
  },
  "/admin/plugins": {
    "permission": "plugins.read"
  },
  "/admin/plugin-instances": {
    "permission": "plugin_instances.read"
  },
  "/admin/plugin-bindings": { permission: "plugin_instances.read" },
  "/admin/plugin-tasks": { permission: "plugin_tasks.read" },
  "/admin/plugin-runs": { permission: "plugin_runs.read" },
  "/admin/announcements": {
    "permission": "announcements.read"
  },
  "/admin/proxies": {
    "permission": "proxies.read"
  },
  "/admin/redeem": {
    "permission": "redeem_codes.read"
  },
  "/admin/promo-codes": {
    "permission": "promo_codes.read"
  },
  "/admin/settings": {
    "permission": "settings.read"
  },
  "/admin/risk-control": {
    "permission": "risk_control.read"
  },
  "/admin/prompt-audit": {
    "permission": "prompt_audit.read"
  },
  "/admin/usage": {
    "permission": "usage.read"
  },
  "/admin/affiliates": {
    "permission": "affiliates.read"
  },
  "/admin/affiliates/invites": {
    "permission": "affiliates.read"
  },
  "/admin/affiliates/rebates": {
    "permission": "affiliates.read"
  },
  "/admin/affiliates/transfers": {
    "permission": "affiliates.read"
  },
  "/admin/orders/dashboard": {
    "permission": "payments.read"
  },
  "/admin/orders": {
    "permission": "payments.read"
  },
  "/admin/orders/plans": {
    "permission": "payments.read"
  }
}
export function canAccessAdminPath(subject: PermissionSubject, path: string): boolean {
  return allows(subject, adminRoutePolicies[path])
}
export function adminLandingPath(subject: PermissionSubject, available: (path: string) => boolean = () => true): string {
  return ['/admin/dashboard', ...Object.keys(adminRoutePolicies).filter(path => path !== '/admin/dashboard')].find(path => path !== '/admin' && canAccessAdminPath(subject, path) && available(path)) ?? '/access-denied'
}
interface NavigationItem { path: string; children?: NavigationItem[]; expandOnly?: boolean }
export function filterAdminNavigation<T extends NavigationItem>(items: T[], subject: PermissionSubject): T[] {
  return items.flatMap(item => {
    if (item.children) {
      const children = filterAdminNavigation(item.children, subject)
      return children.length ? [{ ...item, children, expandOnly: true } as T] : []
    }
    const allowed = item.path.startsWith('/admin') ? canAccessAdminPath(subject, item.path)
      : item.path.startsWith('/custom/') ? subject.hasPermission('pages.read') : true
    return allowed ? [item] : []
  })
}
