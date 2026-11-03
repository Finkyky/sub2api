import { apiClient } from '../client'

export interface PluginTaskScope {
  channel_ids: number[]
  // Omitted/empty grants channel-wide pricing access; non-empty restricts entry IDs.
  pricing_ids?: number[]
  pricing_fields: string[]
  network_urls: string[]
  kv_keys: string[]
}
export interface PluginTask {
  id: number
  instance_id: number
  name: string
  entry: string
  cron: string
  enabled: boolean
  timeout_seconds: number
  max_retries: number
  retry_backoff_seconds: number
  generation: number
  input: Record<string, unknown>
  scope: PluginTaskScope
  next_run_at: string | null
}
export type CreatePluginTask = Omit<PluginTask, 'id' | 'generation' | 'next_run_at'>
export type UpdatePluginTask = CreatePluginTask & { expected_generation: number }
export interface PluginRun {
  id: number
  task_id: number
  instance_id: number
  config_revision: number
  task_generation: number
  trigger: string
  idempotency_key: string
  attempt: number
  fencing_token: number
  status: string
  changed: boolean
  error_code: string
  started_at: string | null
  finished_at: string | null
  duration_ms: number | null
}
// TaskResult is an opaque raw declaration of operations, never console/log data.
export type TaskResult = Record<string, unknown>
export interface SensitivePluginRun { run: PluginRun; result: TaskResult | null }
const runFields = ['id', 'task_id', 'instance_id', 'config_revision', 'task_generation', 'trigger', 'idempotency_key', 'attempt', 'fencing_token', 'status', 'changed', 'error_code', 'started_at', 'finished_at', 'duration_ms'] as const
export function runMetadata(run: PluginRun): PluginRun {
  return Object.fromEntries(runFields.map(key => [key, run[key]])) as unknown as PluginRun
}
// Deliberate field projection prevents unexpected output/result fields in ordinary APIs.
export const pluginTasksAPI = {
  async list(instanceId?: number): Promise<PluginTask[]> {
    const { data } = await apiClient.get<{ tasks: PluginTask[] | null }>('/admin/plugin-tasks', { params: instanceId ? { instance_id: instanceId } : undefined })
    return data.tasks ?? []
  },
  async get(id: number): Promise<PluginTask> {
    const { data } = await apiClient.get<{ task: PluginTask }>(`/admin/plugin-tasks/${id}`)
    return data.task
  },
  async create(task: CreatePluginTask): Promise<PluginTask> {
    const { data } = await apiClient.post<{ task: PluginTask }>('/admin/plugin-tasks', task)
    return data.task
  },
  async update(id: number, task: UpdatePluginTask): Promise<PluginTask> {
    const { data } = await apiClient.put<{ task: PluginTask }>(`/admin/plugin-tasks/${id}`, task)
    return data.task
  },
  async delete(id: number, expectedGeneration: number): Promise<void> {
    await apiClient.delete(`/admin/plugin-tasks/${id}`, { data: { expected_generation: expectedGeneration } })
  },
  async run(id: number, idempotencyKey: string): Promise<PluginRun> {
    const { data } = await apiClient.post<{ run: PluginRun }>(`/admin/plugin-tasks/${id}/run`, { idempotency_key: idempotencyKey })
    return runMetadata(data.run)
  },
  async runs(id: number): Promise<PluginRun[]> {
    const { data } = await apiClient.get<{ runs: PluginRun[] | null }>(`/admin/plugin-tasks/${id}/runs`, { params: { limit: 50 } })
    return (data.runs ?? []).map(runMetadata)
  }
}
export const pluginRunsAPI = {
  async get(id: number): Promise<PluginRun> {
    const { data } = await apiClient.get<{ run: PluginRun }>(`/admin/plugin-runs/${id}`)
    return runMetadata(data.run)
  },
  async sensitive(id: number): Promise<SensitivePluginRun> {
    const { data } = await apiClient.get<SensitivePluginRun>(`/admin/plugin-runs/${id}/sensitive`)
    return { run: runMetadata(data.run), result: data.result }
  }
}
export default pluginTasksAPI
