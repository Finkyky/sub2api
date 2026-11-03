import type { CreatePluginTask, PluginTaskScope } from '@/api/admin/pluginTasks'
export const emptyScope = (): PluginTaskScope => ({ channel_ids: [], pricing_fields: [], network_urls: [], kv_keys: [] })
export function objectJSON(text: string): Record<string, unknown> | null {
  try { const data = JSON.parse(text); return data && typeof data === 'object' && !Array.isArray(data) ? data : null } catch { return null }
}
export function taskScope(text: string): PluginTaskScope | null {
  const value = objectJSON(text)
  if (!value || Object.keys(value).some(k => !['channel_ids','pricing_ids','pricing_fields','network_urls','kv_keys'].includes(k))) return null
  if (!Array.isArray(value.channel_ids) || !value.channel_ids.every(v => Number.isSafeInteger(v) && v > 0)) return null
  if (value.pricing_ids !== undefined && (!Array.isArray(value.pricing_ids) || !value.pricing_ids.every(v => Number.isSafeInteger(v) && v > 0) || new Set(value.pricing_ids).size !== value.pricing_ids.length)) return null
  for (const key of ['pricing_fields','network_urls','kv_keys']) if (!Array.isArray(value[key]) || !(value[key] as unknown[]).every(v => typeof v === 'string' && v.trim().length > 0)) return null
  return value as unknown as PluginTaskScope
}
export function taskValid(task: CreatePluginTask): boolean {
  return Number.isSafeInteger(task.instance_id) && task.instance_id > 0 && !!task.name.trim() && !!task.entry.trim()
    && Number.isSafeInteger(task.timeout_seconds) && task.timeout_seconds > 0
    && Number.isSafeInteger(task.max_retries) && task.max_retries >= 0
    && Number.isSafeInteger(task.retry_backoff_seconds) && task.retry_backoff_seconds >= 0
}
