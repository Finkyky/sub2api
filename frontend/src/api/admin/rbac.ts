import { apiClient } from '../client'

export interface RBACRole {
  id: number
  code: string
  name: string
  description: string
  is_system: boolean
  is_super_admin: boolean
  permissions: string[]
  created_at?: string
  updated_at?: string
}
export interface RBACPermission {
  code: string
  name: string
  resource: string
  action: string
  description: string
}
export interface RoleMetadata { name: string; description: string }
export interface CreateRoleInput extends RoleMetadata { code: string }

// apiClient unwraps the envelope. RBAC list endpoints return arrays, not pages.
export const rbacAPI = {
  async listRoles(): Promise<RBACRole[]> {
    return (await apiClient.get<RBACRole[]>('/admin/roles')).data
  },
  async getRole(id: number): Promise<RBACRole> {
    return (await apiClient.get<RBACRole>(`/admin/roles/${id}`)).data
  },
  async createRole(input: CreateRoleInput): Promise<RBACRole> {
    return (await apiClient.post<RBACRole>('/admin/roles', input)).data
  },
  async updateRole(id: number, input: RoleMetadata): Promise<RBACRole> {
    return (await apiClient.put<RBACRole>(`/admin/roles/${id}`, input)).data
  },
  async deleteRole(id: number): Promise<void> {
    await apiClient.delete(`/admin/roles/${id}`)
  },
  async listPermissions(): Promise<RBACPermission[]> {
    return (await apiClient.get<RBACPermission[]>('/admin/permissions')).data
  },
  async getRolePermissions(id: number): Promise<string[]> {
    return (await apiClient.get<string[]>(`/admin/roles/${id}/permissions`)).data
  },
  async setRolePermissions(id: number, permissions: string[]): Promise<void> {
    await apiClient.put(`/admin/roles/${id}/permissions`, { permissions })
  },
  async getUserRoles(id: number): Promise<RBACRole[]> {
    return (await apiClient.get<RBACRole[]>(`/admin/users/${id}/roles`)).data
  },
  async setUserRoles(id: number, role_ids: number[]): Promise<void> {
    await apiClient.put(`/admin/users/${id}/roles`, { role_ids })
  }
}
export default rbacAPI
