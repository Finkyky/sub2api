import { beforeEach, describe, expect, it, vi } from 'vitest'
import { pluginInstancesAPI } from '../pluginInstances'
const client = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), put: vi.fn() }))
vi.mock('@/api/client', () => ({ apiClient: client }))
const wire = { instance: { id: 7, plugin_id: 3, name: 'Test', generation: 4, current_revision: 1, latest_revision: 2, enabled: false }, revision: { instance_id: 7, revision: 1, config_version: 1 }, manifest: {}, bindings: null }
beforeEach(() => { vi.resetAllMocks(); for (const mock of Object.values(client)) mock.mockResolvedValue({ data: wire }) })
describe('nullable binding projection', () => {
  it.each(['get', 'create', 'saveConfig', 'publish', 'disable', 'replaceBindings'] as const)('normalizes null bindings from %s', async method => {
    const requests = {
      get: () => pluginInstancesAPI.get(7),
      create: () => pluginInstancesAPI.create({ plugin_id: 3, name: 'Test', config: {} }),
      saveConfig: () => pluginInstancesAPI.saveConfig(7, { expected_generation: 4, config: {} }),
      publish: () => pluginInstancesAPI.publish(7, { expected_generation: 4, revision: 1 }),
      disable: () => pluginInstancesAPI.disable(7, 4),
      replaceBindings: () => pluginInstancesAPI.replaceBindings(7, { expected_generation: 4, bindings: [] })
    }
    expect((await requests[method]()).bindings).toEqual([])
    expect(wire.bindings).toBeNull()
  })
  it('preserves valid binding arrays', async () => {
    const bindings = [{ instance_id: 7, group_id: 12, stage: 'input', entry: 'check', protocol: 'openai.chat', sort_order: 0, enabled: true, can_replace: false }]
    client.get.mockResolvedValue({ data: { ...wire, bindings } })
    expect((await pluginInstancesAPI.get(7)).bindings).toEqual(bindings)
  })
})
