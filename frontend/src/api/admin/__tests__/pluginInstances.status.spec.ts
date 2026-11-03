import { beforeEach, describe, expect, it, vi } from 'vitest'
import { pluginInstancesAPI } from '@/api/admin/pluginInstances'
import { adminOperations } from '@/permissions/adminOperations'
import { allows } from '@/permissions/policy'

const client = vi.hoisted(() => ({ get: vi.fn() }))
vi.mock('@/api/client', () => ({ apiClient: client }))
beforeEach(() => { vi.resetAllMocks() })

describe('plugin instance node-local status contract', () => {
  it('reads status through the exact read-only endpoint', async () => {
    const status = { instance_id: 11, enabled: true, current_revision: 2,
      runtime_present: false, lease_refs: 0, retired: false, closed: true,
      last_error: '', runtimes: [] }
    client.get.mockResolvedValue({ data: status })
    expect(await pluginInstancesAPI.status(11)).toEqual(status)
    expect(client.get).toHaveBeenCalledTimes(1)
    expect(client.get).toHaveBeenLastCalledWith('/admin/plugin-instances/11/status')
  })
  it('requires instance read permission and reacts to revocation', () => {
    const policy = adminOperations['GET /api/v1/admin/plugin-instances/:id/status']
    expect(policy).toEqual({ permission: 'plugin_instances.read' })
    const reader = { isSuperAdmin: false, hasPermission: (code: string) => code === 'plugin_instances.read' }
    expect(allows(reader, policy)).toBe(true)
    expect(allows({ ...reader, hasPermission: () => false }, policy)).toBe(false)
    expect(allows({ ...reader, hasPermission: (code: string) => code === 'plugins.read' }, policy)).toBe(false)
  })
  it('propagates status read failures instead of returning a healthy default', async () => {
    const error = new Error('status unavailable')
    client.get.mockRejectedValue(error)
    await expect(pluginInstancesAPI.status(11)).rejects.toBe(error)
  })
})
