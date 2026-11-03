<template>
  <section class="space-y-4">
    <header class="flex flex-wrap items-end justify-between gap-3 border-b border-gray-200 pb-4 dark:border-dark-700"><div><h2 class="text-lg font-semibold">{{ t('admin.pluginRuns.title') }}</h2><p class="mt-1 text-sm text-gray-500">{{ t('admin.pluginRuns.description') }}</p></div><button v-if="canRead" class="btn btn-secondary" :disabled="loading || busy" @click="load">{{ t('admin.pluginInstances.refresh') }}</button></header>
    <label v-if="canRead && !taskId" class="block max-w-xs text-sm">{{ t('admin.pluginRuns.taskId') }}<input v-model="filter" data-testid="run-task-filter" class="input mt-1 w-full" type="number" min="1" @change="changeFilter" /></label>
    <p v-if="error" role="alert" class="rounded-lg bg-red-50 p-3 text-sm text-red-700 dark:bg-red-950/20 dark:text-red-300">{{ error }}</p>
    <p v-if="!canRead" class="py-8 text-center text-sm text-gray-500">{{ t('admin.pluginRuns.readDenied') }}</p>
    <p v-else-if="loading" role="status" class="py-8 text-center text-sm text-gray-500">{{ t('admin.pluginInstances.loading') }}</p>
    <p v-else-if="!effectiveTaskId" class="py-8 text-center text-sm text-gray-500">{{ t('admin.pluginRuns.chooseTask') }}</p>
    <p v-else-if="!runs.length" class="rounded-lg border border-dashed border-gray-300 p-8 text-center text-sm text-gray-500 dark:border-dark-700">{{ t('admin.pluginRuns.empty') }}</p>
    <div v-else class="overflow-x-auto rounded-lg border border-gray-200 dark:border-dark-700"><table class="min-w-full divide-y divide-gray-200 text-sm dark:divide-dark-700">
      <thead class="bg-gray-50 dark:bg-dark-800"><tr><th v-for="key in ['id','status','trigger','attempt','config_revision','task_generation','changed','duration_ms','started_at','actions']" :key="key" class="px-4 py-3 text-left">{{ t('admin.pluginRuns.' + key) }}</th></tr></thead>
      <tbody class="divide-y divide-gray-200 bg-white dark:divide-dark-700 dark:bg-dark-900"><tr v-for="run in runs" :key="run.id"><td class="px-4 py-3">#{{ run.id }}</td><td class="px-4 py-3">{{ run.status }}</td><td class="px-4 py-3">{{ run.trigger }}</td><td class="px-4 py-3">{{ run.attempt }}</td><td class="px-4 py-3">{{ run.config_revision }}</td><td class="px-4 py-3">{{ run.task_generation }}</td><td class="px-4 py-3">{{ t(run.changed ? 'admin.pluginInstances.yes' : 'admin.pluginInstances.no') }}</td><td class="px-4 py-3">{{ run.duration_ms ?? '—' }}</td><td class="px-4 py-3 text-xs">{{ run.started_at || '—' }}</td><td class="px-4 py-3"><button data-testid="run-details" class="btn btn-secondary btn-sm" :disabled="detailLoading || busy" @click="openRun(run.id)">{{ t('admin.pluginInstances.details') }}</button></td></tr></tbody>
    </table></div>
    <BaseDialog :show="!!selected && canRead" :title="t('admin.pluginRuns.details')" @close="closeDetail">
      <div v-if="selected" class="space-y-4">
        <dl class="grid gap-3 sm:grid-cols-2"><div v-for="(value, key) in selected" :key="key"><dt class="text-xs text-gray-500">{{ t('admin.pluginRuns.' + key) }}</dt><dd class="break-all text-sm">{{ value ?? '—' }}</dd></div></dl>
        <p class="text-xs text-gray-500">{{ t('admin.pluginRuns.redacted') }}</p>
        <button v-if="canSensitive" data-testid="show-sensitive" class="btn btn-secondary" :disabled="busy" @click="readSensitive">{{ t('admin.pluginRuns.showSensitive') }}</button>
        <button v-if="rawResult !== null && canSensitive" data-testid="hide-sensitive" class="btn btn-secondary" @click="clearSensitive">{{ t('admin.pluginRuns.hideSensitive') }}</button>
        <pre v-if="rawResult !== null && canSensitive" data-testid="sensitive-result" class="max-h-96 overflow-auto whitespace-pre-wrap break-all rounded-lg bg-gray-50 p-3 font-mono text-xs dark:bg-dark-800">{{ JSON.stringify(rawResult, null, 2) }}</pre>
        <p v-if="detailError" role="alert" class="text-sm text-red-600">{{ detailError }}</p>
      </div>
    </BaseDialog>
    <TotpStepUpDialog :controller="stepUp" />
  </section>
</template>
<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import TotpStepUpDialog from '@/components/auth/TotpStepUpDialog.vue'
import { useAuthStore } from '@/stores/auth'
import { pluginTasksAPI, pluginRunsAPI, runMetadata, type PluginRun, type TaskResult } from '@/api/admin/pluginTasks'
import { isStepUpCancelled, isStepUpBlocked, stepUpBlockReason } from '@/composables/useStepUp'
import { usePluginOperation } from './usePluginOperation'
const props = defineProps<{ taskId?: number; initialRunId?: number }>()
const { t } = useI18n(), auth = useAuthStore()
const canRead = computed(() => auth.hasPermission('plugin_runs.read')), canSensitive = computed(() => canRead.value && auth.hasPermission('plugin_runs.sensitive'))
const { stepUp, busy, execute, cancel } = usePluginOperation(() => canSensitive.value)
const runs = ref<PluginRun[]>([]), selected = ref<PluginRun | null>(null), rawResult = ref<TaskResult | null>(null)
const loading = ref(false), detailLoading = ref(false), error = ref(''), detailError = ref(''), filter = ref('')
const effectiveTaskId = computed(() => props.taskId || Number(filter.value) || undefined)
let alive = true, listEpoch = 0, detailEpoch = 0, sensitiveEpoch = 0
function feedback(e: unknown) { if (isStepUpBlocked(e)) return t(stepUpBlockReason(e) === 'STEP_UP_ADMIN_API_KEY_FORBIDDEN' ? 'stepUp.adminApiKeyForbidden' : 'stepUp.notEnabled'); return t('admin.pluginTasks.requestFailed') }
function clearSensitive() { sensitiveEpoch++; cancel(); rawResult.value = null; detailError.value = '' }
function closeDetail() { detailEpoch++; clearSensitive(); selected.value = null; detailLoading.value = false }
async function load() {
  if (!canRead.value || !alive || !effectiveTaskId.value) return
  const id = effectiveTaskId.value
  if (!Number.isSafeInteger(id) || id <= 0) { error.value = t('admin.pluginTasks.invalid'); return }
  const epoch = ++listEpoch; loading.value = true; error.value = ''
  try { const values = await pluginTasksAPI.runs(id); if (alive && canRead.value && epoch === listEpoch) runs.value = values.map(runMetadata) }
  catch (e) { if (alive && canRead.value && epoch === listEpoch) error.value = feedback(e) }
  finally { if (epoch === listEpoch) loading.value = false }
}
async function openRun(id: number) {
  if (!canRead.value || busy.value || !alive) return
  closeDetail(); const epoch = detailEpoch; detailLoading.value = true
  try { const run = await pluginRunsAPI.get(id); if (alive && canRead.value && epoch === detailEpoch) selected.value = runMetadata(run) }
  catch (e) { if (alive && canRead.value && epoch === detailEpoch) error.value = feedback(e) }
  finally { if (epoch === detailEpoch) detailLoading.value = false }
}
async function readSensitive() {
  if (!canSensitive.value || !selected.value || busy.value) return
  clearSensitive(); const epoch = sensitiveEpoch, detail = detailEpoch, id = selected.value.id
  try {
    const result = await execute('sensitive', () => pluginRunsAPI.sensitive(id), () => epoch === sensitiveEpoch && detail === detailEpoch && selected.value?.id === id)
    if (alive && canSensitive.value && epoch === sensitiveEpoch && detail === detailEpoch && selected.value?.id === id && result.run.id === id) rawResult.value = result.result
  } catch (e) { if (!isStepUpCancelled(e) && alive && canSensitive.value && epoch === sensitiveEpoch) detailError.value = feedback(e) }
}
function changeFilter() { listEpoch++; runs.value = []; closeDetail(); error.value = ''; void load() }
watch(canSensitive, value => { if (!value) clearSensitive() }, { flush: 'sync' })
watch(canRead, value => { if (!value) { listEpoch++; runs.value = []; filter.value = ''; loading.value = false; error.value = ''; closeDetail() } else void load() }, { flush: 'sync' })
watch(() => props.taskId, changeFilter, { flush: 'sync' })
watch(() => props.initialRunId, id => { closeDetail(); if (id) void openRun(id) }, { flush: 'sync' })
onMounted(() => { void load(); if (props.initialRunId) void openRun(props.initialRunId) })
onBeforeUnmount(() => { alive = false; listEpoch++; runs.value = []; closeDetail() })
</script>
