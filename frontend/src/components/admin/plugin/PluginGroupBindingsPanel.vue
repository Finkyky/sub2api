<template>
  <section class="space-y-4">
    <header class="flex flex-wrap items-end justify-between gap-3 border-b border-gray-200 pb-4 dark:border-dark-700"><div><h2 class="text-lg font-semibold">{{ t('admin.pluginBindings.title') }}</h2><p class="mt-1 text-sm text-gray-500">{{ t('admin.pluginBindings.description') }}</p></div><button v-if="canRead" class="btn btn-secondary" :disabled="loading" @click="load">{{ t('admin.pluginInstances.refresh') }}</button></header>
    <p v-if="!canRead" class="text-sm text-gray-500">{{ t('admin.pluginInstances.readDenied') }}</p>
    <p v-else-if="loading" role="status" class="text-sm text-gray-500">{{ t('admin.pluginInstances.loading') }}</p>
    <p v-else-if="error" role="alert" class="text-sm text-red-600">{{ error }}</p>
    <template v-else>
      <p class="text-xs text-gray-500">{{ t('admin.pluginBindings.snapshotHint') }}</p>
      <div v-for="route in blockedRoutes" :key="route.key" data-testid="chain-blocked" role="alert" class="rounded-lg border border-amber-200 bg-amber-50 p-3 text-sm text-amber-800 dark:border-amber-900/50 dark:bg-amber-950/20 dark:text-amber-300">
        {{ t('admin.pluginInstances.group') }} #{{ route.groupId }} · {{ route.protocol }} · {{ t('admin.pluginBindings.blocked') }}
        <p class="mt-1 text-xs">{{ t('admin.pluginBindings.blockedHint') }}</p>
      </div>
      <div data-testid="effective-chain" class="space-y-3">
        <div v-for="stage in stages" :key="stage" class="rounded-lg border border-gray-200 p-4 dark:border-dark-700">
          <h3 class="text-sm font-semibold">{{ t('admin.pluginBindings.' + stage) }}</h3>
          <p v-if="!chains.some(chain => chain.stage === stage)" class="mt-2 text-sm text-gray-500">{{ t('admin.pluginBindings.empty') }}</p>
          <div v-for="chain in chains.filter(chain => chain.stage === stage)" :key="chain.key" class="mt-3">
            <p class="text-xs font-medium text-gray-500">{{ t('admin.pluginInstances.group') }} #{{ chain.groupId }} · {{ chain.protocol }} <span data-testid="chain-status" :class="chain.blocked ? 'text-amber-700 dark:text-amber-300' : 'text-emerald-700 dark:text-emerald-300'">· {{ t(chain.blocked ? 'admin.pluginBindings.blocked' : 'admin.pluginBindings.effective') }}</span></p>
            <ol class="mt-2 flex flex-wrap items-center gap-2"><li v-for="(row, index) in chain.rows" :key="row.key" class="flex items-center gap-2"><span v-if="index" aria-hidden="true" class="text-gray-400">→</span><span class="rounded-lg bg-gray-50 px-3 py-2 text-xs dark:bg-dark-800"><span class="font-medium">{{ row.instanceName }}</span> · {{ row.binding.entry }}<br />#{{ row.binding.instance_id }} · r{{ row.revision }} · {{ t('admin.pluginInstances.sortOrder') }} {{ row.binding.sort_order }}<span v-if="row.binding.can_replace"> · {{ t('admin.pluginInstances.canReplace') }}</span><span v-if="row.blocker" data-testid="binding-blocker" class="mt-1 block font-semibold text-amber-700 dark:text-amber-300">{{ t('admin.pluginBindings.' + row.blocker) }}</span></span></li></ol>
          </div>
        </div>
      </div>
      <details v-if="rows.length" class="rounded-lg border border-gray-200 p-4 dark:border-dark-700"><summary class="cursor-pointer text-sm font-medium">{{ t('admin.pluginBindings.configured') }} ({{ rows.length }})</summary>
        <ul class="mt-3 space-y-2 text-sm"><li v-for="row in rows" :key="row.key" class="flex flex-wrap items-center justify-between gap-2"><span>{{ row.instanceName }} · #{{ row.binding.group_id }} · {{ t('admin.pluginBindings.' + row.binding.stage) }} · {{ row.binding.protocol }} · {{ row.binding.entry }} · {{ t('admin.pluginBindings.' + rowState(row)) }}</span><button v-if="canEdit" data-testid="edit-binding-instance" class="btn btn-secondary btn-sm" @click="editInstance = row.binding.instance_id">{{ t('admin.pluginInstances.details') }}</button></li></ul>
      </details>
      <div v-if="editInstance && canEdit" class="rounded-lg border border-gray-200 p-4 dark:border-dark-700"><button class="btn btn-secondary btn-sm mb-3" @click="editInstance = undefined; load()">{{ t('admin.pluginInstances.close') }}</button><PluginInstancesPanel :initial-instance-id="editInstance" :plugin-id="pluginId" /></div>
    </template>
  </section>
</template>
<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAuthStore } from '@/stores/auth'
import { adminAPI } from '@/api/admin'
import type { PluginGroupBinding, PluginInstanceDetail } from '@/api/admin/pluginInstances'
import PluginInstancesPanel from './PluginInstancesPanel.vue'
const props = defineProps<{ groupId?: number; pluginId?: number }>()
const { t } = useI18n(), auth = useAuthStore()
const canRead = computed(() => auth.hasPermission('plugin_instances.read') && (!props.groupId || auth.hasPermission('groups.read')))
const canEdit = computed(() => canRead.value && auth.hasPermission('plugin_instances.bindings.update'))
const stages = ['input','output'] as const
interface Row { key: string; instanceName: string; binding: PluginGroupBinding; revision: number; blocker: 'disabled' | 'unpublished' | 'unavailable' | null }
const rows = ref<Row[]>([]), loading = ref(false), error = ref(''), editInstance = ref<number>()
let epoch = 0, alive = true
// Enabled bindings remain selected even when their instance is unavailable.
// Prepare pins both stages; any blocker rejects the same group/protocol route.
const blockedRoutes = computed(() => {
  const routes = new Map<string, { key: string; groupId: number; protocol: string }>()
  for (const row of rows.value) {
    if (!row.binding.enabled || !row.blocker) continue
    const { group_id: groupId, protocol } = row.binding, key = `${groupId}:${protocol}`
    routes.set(key, { key, groupId, protocol })
  }
  return [...routes.values()].sort((a,b) => a.groupId - b.groupId || a.protocol.localeCompare(b.protocol))
})
function routeBlocked(binding: PluginGroupBinding) { return blockedRoutes.value.some(route => route.groupId === binding.group_id && route.protocol === binding.protocol) }
function rowState(row: Row) { return !row.binding.enabled ? 'inactive' : row.blocker || (routeBlocked(row.binding) ? 'blocked' : 'effective') }
const chains = computed(() => {
  const grouped = new Map<string, { key: string; stage: PluginGroupBinding['stage']; groupId: number; protocol: string; rows: Row[] }>()
  for (const row of rows.value.filter(r => r.binding.enabled)) {
    const b = row.binding, key = `${b.group_id}:${b.stage}:${b.protocol}`
    if (!grouped.has(key)) grouped.set(key, { key, stage: b.stage, groupId: b.group_id, protocol: b.protocol, rows: [] })
    grouped.get(key)!.rows.push(row)
  }
  return [...grouped.values()].sort((a,b) => a.groupId - b.groupId || a.protocol.localeCompare(b.protocol)).map(chain => ({ ...chain, blocked: routeBlocked(chain.rows[0]!.binding), rows: chain.rows.sort((a,b) => a.binding.sort_order - b.binding.sort_order || (a.binding.id || 0) - (b.binding.id || 0)) }))
})
function project(details: PluginInstanceDetail[]): Row[] {
  return details.flatMap(detail => detail.bindings.filter(b => !props.groupId || b.group_id === props.groupId).map((binding, i) => ({
    key: `${detail.instance.id}:${binding.id ?? i}`, instanceName: detail.instance.name, binding, revision: detail.instance.current_revision,
    blocker: !detail.instance.enabled ? 'disabled' : detail.instance.current_revision <= 0 ? 'unpublished' : binding.instance_id !== detail.instance.id || (binding.id || 0) <= 0 ? 'unavailable' : null
  })))
}
async function load() {
  if (!canRead.value || !alive) return
  const token = ++epoch; loading.value = true; rows.value = []; error.value = ''
  const valid = () => alive && canRead.value && token === epoch
  try {
    const instances = await adminAPI.pluginInstances.list(props.pluginId)
    if (!valid()) return
    // Limit in-flight detail requests; any failure prevents an incomplete chain.
    const details: PluginInstanceDetail[] = []
    for (let i = 0; i < instances.length; i += 8) {
      if (!valid()) return
      const batch = await Promise.all(instances.slice(i,i+8).map(instance => adminAPI.pluginInstances.get(instance.id)))
      if (!valid()) return
      details.push(...batch)
    }
    if (valid()) rows.value = project(details)
  } catch { if (valid()) { rows.value = []; error.value = t('admin.pluginBindings.loadFailed') } }
  finally { if (token === epoch) loading.value = false }
}
watch(canRead, value => { epoch++; rows.value = []; editInstance.value = undefined; error.value = ''; loading.value = false; if (value) void load() }, { flush: 'sync' })
watch(canEdit, value => { if (!value) editInstance.value = undefined }, { flush: 'sync' })
watch(() => [props.groupId, props.pluginId], () => { epoch++; rows.value = []; editInstance.value = undefined; void load() }, { flush: 'sync' })
onMounted(load)
onBeforeUnmount(() => { alive = false; epoch++; rows.value = [] })
</script>
