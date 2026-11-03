import type { PluginGroupBinding, PluginHookDeclaration } from '@/api/admin/pluginInstances'

export function bindingsValid(bindings: PluginGroupBinding[], hooks: PluginHookDeclaration[], instanceId: number): boolean {
  if (bindings.length > 128) return false
  const keys = new Set<string>()
  return bindings.every(binding => {
    const hook = hooks.find(item => item.name === binding.entry)
    const key = JSON.stringify([binding.group_id, binding.stage, binding.entry, binding.protocol])
    if (!Number.isSafeInteger(binding.group_id) || binding.group_id <= 0 ||
        binding.instance_id !== instanceId || !Number.isSafeInteger(binding.sort_order) || Math.abs(binding.sort_order) > 100000 ||
        typeof binding.enabled !== 'boolean' || typeof binding.can_replace !== 'boolean' ||
        !hook?.stages.includes(binding.stage) || !hook.protocols.includes(binding.protocol) ||
        (binding.can_replace && !hook.can_replace) || keys.has(key)) return false
    keys.add(key)
    return true
  })
}

export function parseConfig(text: string): Record<string, unknown> | null {
  try {
    const value: unknown = JSON.parse(text)
    return value !== null && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : null
  } catch {
    return null
  }
}
