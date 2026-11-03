import { beforeEach, describe, expect, it, vi } from 'vitest'
import { pluginInstancesAPI } from '@/api/admin/pluginInstances'
import { adminOperations } from '@/permissions/adminOperations'
import { allows } from '@/permissions/policy'

const client = vi.hoisted(() => ({ delete: vi.fn() }))
vi.mock('@/api/client', () => ({ apiClient: client }))
beforeEach(() => { vi.resetAllMocks() })

describe('plugin instance delete contract', () => {
  it('sends generation in DELETE body and returns the deletion receipt', async () => {
    client.delete.mockResolvedValue({ data: { deleted: true } })
    expect(await pluginInstancesAPI.delete(11, 3)).toEqual({ deleted: true })
    expect(client.delete).toHaveBeenCalledTimes(1)
    expect(client.delete).toHaveBeenLastCalledWith('/admin/plugin-instances/11', { data: { expected_generation: 3 } })
  })
  it('requires exact delete permission, never package or update permission', () => {
    const policy = adminOperations['DELETE /api/v1/admin/plugin-instances/:id']
    expect(policy).toEqual({ permission: 'plugin_instances.delete' })
    const principal = { isSuperAdmin: false, hasPermission: (code: string) => code === 'plugin_instances.delete' }
    expect(allows(principal, policy)).toBe(true)
    for (const wrong of ['plugins.delete', 'plugin_instances.update', 'plugin_instances.disable']) {
      expect(allows({ ...principal, hasPermission: (code: string) => code === wrong }, policy)).toBe(false)
    }
    expect(allows({ ...principal, hasPermission: () => false }, policy)).toBe(false)
  })
  it('propagates deletion failure without fabricating success', async () => {
    const failure = new Error('generation conflict')
    client.delete.mockRejectedValue(failure)
    await expect(pluginInstancesAPI.delete(11, 3)).rejects.toBe(failure)
  })
})
