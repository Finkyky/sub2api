import { beforeEach, describe, expect, it, vi } from 'vitest'
import { rbacAPI } from '@/api/admin/rbac'
const client = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), put: vi.fn(), delete: vi.fn() }))
vi.mock('@/api/client', () => ({ apiClient: client }))
beforeEach(() => { vi.resetAllMocks(); Object.values(client).forEach(fn => fn.mockResolvedValue({ data: [] })) })
describe('RBAC HTTP contract', () => {
  it('uses all ten backend endpoints without envelope assumptions', async () => {
    await rbacAPI.listRoles(); expect(client.get).toHaveBeenLastCalledWith('/admin/roles')
    await rbacAPI.getRole(2); expect(client.get).toHaveBeenLastCalledWith('/admin/roles/2')
    await rbacAPI.listPermissions(); expect(client.get).toHaveBeenLastCalledWith('/admin/permissions')
    await rbacAPI.getRolePermissions(2); expect(client.get).toHaveBeenLastCalledWith('/admin/roles/2/permissions')
    await rbacAPI.getUserRoles(3); expect(client.get).toHaveBeenLastCalledWith('/admin/users/3/roles')
    const create = { code: 'a', name: 'A', description: '' }
    await rbacAPI.createRole(create); expect(client.post).toHaveBeenLastCalledWith('/admin/roles', create)
    const update = { name: 'B', description: '' }
    await rbacAPI.updateRole(2, update); expect(client.put).toHaveBeenLastCalledWith('/admin/roles/2', update)
    await rbacAPI.deleteRole(2); expect(client.delete).toHaveBeenLastCalledWith('/admin/roles/2')
    await rbacAPI.setRolePermissions(2, []); expect(client.put).toHaveBeenLastCalledWith('/admin/roles/2/permissions', { permissions: [] })
    await rbacAPI.setUserRoles(3, []); expect(client.put).toHaveBeenLastCalledWith('/admin/users/3/roles', { role_ids: [] })
  })
})
