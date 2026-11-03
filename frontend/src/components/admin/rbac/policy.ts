import type { RBACRole } from '@/api/admin/rbac'
import type { RoleSummary } from '@/types'
export interface Authority {
  isSuperAdmin: boolean
  hasPermission: (code: string) => boolean
}
export const isSuperRole = (role: Pick<RBACRole, 'code'> & { is_super_admin?: boolean }) =>
  role.is_super_admin === true || role.code === 'super_admin'
export const isProtectedRole = (role: RBACRole) => role.is_system || isSuperRole(role)
export const withinCeiling = (authority: Authority, role: RBACRole) => authority.isSuperAdmin ||
  (!isSuperRole(role) && (role.permissions ?? []).every(code => authority.hasPermission(code)))
export const canManageRole = (authority: Authority, role: RBACRole) =>
  !isProtectedRole(role) && withinCeiling(authority, role)
export const isProtectedUser = (user: { role?: string; is_super_admin?: boolean; roles?: RoleSummary[] }) =>
  user.role === 'admin' || user.is_super_admin === true || user.roles?.some(isSuperRole) === true
export function rbacError(error: unknown, fallback: string): string {
  const e = error as { status?: number; message?: string; response?: { data?: { detail?: string; message?: string } } } | null
  return e?.response?.data?.detail || e?.response?.data?.message || (typeof e?.status === 'number' ? e.message : '') || fallback
}
