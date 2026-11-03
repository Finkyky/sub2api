import { beforeEach, describe, expect, it, vi } from 'vitest'
import { pluginTasksAPI, pluginRunsAPI } from '../pluginTasks'
const client = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), put: vi.fn(), delete: vi.fn() }))
vi.mock('@/api/client', () => ({ apiClient: client }))
const task = { id: 3, instance_id: 7, name: 'Sync', entry: 'sync', cron: '0 * * * *', enabled: true, timeout_seconds: 30, max_retries: 2, retry_backoff_seconds: 5, generation: 4, input: {}, scope: { channel_ids: [], pricing_fields: [], network_urls: [], kv_keys: [] }, next_run_at: null }
const run = { id: 9, task_id: 3, instance_id: 7, config_revision: 1, task_generation: 4, trigger: 'manual', idempotency_key: 'key', attempt: 1, fencing_token: 2, status: 'succeeded', changed: false, error_code: '', started_at: null, finished_at: null, duration_ms: 0 }
beforeEach(() => vi.resetAllMocks())
describe('M6 frozen task/run HTTP contract', () => {
  it('unwraps task list/detail and normalizes null lists', async () => {
    client.get.mockResolvedValueOnce({ data: { tasks: [task] } }).mockResolvedValueOnce({ data: { task } }).mockResolvedValueOnce({ data: { tasks: null } })
    expect(await pluginTasksAPI.list(7)).toEqual([task]); expect(client.get).toHaveBeenCalledWith('/admin/plugin-tasks', { params: { instance_id: 7 } })
    expect(await pluginTasksAPI.get(3)).toEqual(task); expect(client.get).toHaveBeenCalledWith('/admin/plugin-tasks/3')
    expect(await pluginTasksAPI.list()).toEqual([])
  })
  it('creates without id/generation/next_run_at and updates with expected_generation', async () => {
    client.post.mockResolvedValue({ data: { task } }); client.put.mockResolvedValue({ data: { task } })
    const { id: _id, generation: _gen, next_run_at: _next, ...body } = task
    expect(await pluginTasksAPI.create(body)).toEqual(task); expect(client.post).toHaveBeenCalledWith('/admin/plugin-tasks', body)
    expect(await pluginTasksAPI.update(3, { ...body, expected_generation: 4 })).toEqual(task)
    expect(client.put).toHaveBeenCalledWith('/admin/plugin-tasks/3', { ...body, expected_generation: 4 })
    await pluginTasksAPI.delete(3, 4); expect(client.delete).toHaveBeenCalledWith('/admin/plugin-tasks/3', { data: { expected_generation: 4 } })
  })
  it('uses one caller-supplied idempotency key and unwraps runs/detail/sensitive envelopes', async () => {
    client.post.mockResolvedValue({ data: { run } })
    expect(await pluginTasksAPI.run(3, 'key')).toEqual(run); expect(client.post).toHaveBeenCalledWith('/admin/plugin-tasks/3/run', { idempotency_key: 'key' })
    client.get.mockResolvedValueOnce({ data: { runs: [run] } }).mockResolvedValueOnce({ data: { run } }).mockResolvedValueOnce({ data: { run, result: { operations: [{ secret: 'RAW' }] } } })
    expect(await pluginTasksAPI.runs(3)).toEqual([run]); expect(client.get).toHaveBeenCalledWith('/admin/plugin-tasks/3/runs', { params: { limit: 50 } })
    expect(await pluginRunsAPI.get(9)).toEqual(run); expect(client.get).toHaveBeenCalledWith('/admin/plugin-runs/9')
    expect((await pluginRunsAPI.sensitive(9)).result).toEqual({ operations: [{ secret: 'RAW' }] }); expect(client.get).toHaveBeenCalledWith('/admin/plugin-runs/9/sensitive')
  })
  it('projects ordinary run responses to metadata even if raw fields arrive', async () => {
    client.get.mockResolvedValue({ data: { run: { ...run, output: 'SECRET', result: { secret: true } } } })
    expect(await pluginRunsAPI.get(9)).toEqual(run)
  })
})

describe('review pricing_ids wire compatibility', () => {
  it.each([undefined, [], [11, 12]])('preserves optional pricing_ids %j in create/update/detail', async ids => {
    const scope = ids === undefined ? task.scope : { ...task.scope, pricing_ids: ids }
    const value = { ...task, scope, cron: '' }; const { id: _id, generation: _gen, next_run_at: _next, ...body } = value
    client.post.mockResolvedValue({ data: { task: value } }); client.put.mockResolvedValue({ data: { task: value } }); client.get.mockResolvedValue({ data: { task: value } })
    expect(await pluginTasksAPI.create(body)).toEqual(value); expect(client.post).toHaveBeenCalledWith('/admin/plugin-tasks', body)
    expect(await pluginTasksAPI.update(3, { ...body, expected_generation: 4 })).toEqual(value); expect(client.put).toHaveBeenCalledWith('/admin/plugin-tasks/3', { ...body, expected_generation: 4 })
    expect((await pluginTasksAPI.get(3)).scope).toEqual(scope)
  })
})
