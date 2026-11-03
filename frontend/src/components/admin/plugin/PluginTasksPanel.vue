<template>
  <section class="space-y-4">
    <header class="flex flex-wrap items-end justify-between gap-3 border-b border-gray-200 pb-4 dark:border-dark-700">
      <div><h2 class="text-lg font-semibold">{{ t('admin.pluginTasks.title') }}</h2><p class="mt-1 text-sm text-gray-500">{{ t('admin.pluginTasks.description') }}</p></div>
      <div class="flex gap-2"><button v-if="canRead" class="btn btn-secondary" :disabled="loading || busy" @click="load">{{ t('admin.pluginInstances.refresh') }}</button><button v-if="can('create')" data-testid="create-task" class="btn btn-primary" :disabled="busy || detailLoading" @click="createForm">{{ t('admin.pluginTasks.create') }}</button></div>
    </header>
    <label v-if="canRead && !instanceId" class="block max-w-xs text-sm">{{ t('admin.pluginTasks.instanceFilter') }}<input v-model="filter" data-testid="task-instance-filter" class="input mt-1 w-full" type="number" min="1" @change="changeFilter" /></label>
    <div v-if="acceptedRun && can('run')" data-testid="accepted-run" role="status" class="rounded-lg bg-emerald-50 p-3 text-sm text-emerald-800 dark:bg-emerald-950/20 dark:text-emerald-300">{{ t('admin.pluginTasks.runAccepted') }} #{{ acceptedRun.id }} · {{ acceptedRun.status }}<RouterLink v-if="canRunsRead" :to="{ path: '/admin/plugin-runs', query: { task_id: acceptedRun.task_id, run_id: acceptedRun.id } }" class="ml-3 underline">{{ t('admin.pluginRuns.title') }}</RouterLink></div>
    <p v-if="error" role="alert" class="rounded-lg bg-red-50 p-3 text-sm text-red-700 dark:bg-red-950/20 dark:text-red-300">{{ error }}</p>
    <p v-if="!canRead" class="py-8 text-center text-sm text-gray-500">{{ t('admin.pluginTasks.readDenied') }}</p>
    <p v-else-if="loading" role="status" class="py-8 text-center text-sm text-gray-500">{{ t('admin.pluginInstances.loading') }}</p>
    <p v-else-if="!tasks.length" class="rounded-lg border border-dashed border-gray-300 p-8 text-center text-sm text-gray-500 dark:border-dark-700">{{ t('admin.pluginTasks.empty') }}</p>
    <div v-else class="overflow-x-auto rounded-lg border border-gray-200 dark:border-dark-700">
      <table class="min-w-full divide-y divide-gray-200 text-sm dark:divide-dark-700">
        <thead class="bg-gray-50 dark:bg-dark-800"><tr><th v-for="key in ['name','instance','schedule','generation','nextRun','state','actions']" :key="key" class="px-4 py-3 text-left">{{ t('admin.pluginTasks.' + key) }}</th></tr></thead>
        <tbody class="divide-y divide-gray-200 bg-white dark:divide-dark-700 dark:bg-dark-900"><tr v-for="task in tasks" :key="task.id">
          <td class="px-4 py-3"><span class="font-medium">{{ task.name }}</span><div class="text-xs text-gray-500">#{{ task.id }} · {{ task.entry }}</div></td><td class="px-4 py-3">#{{ task.instance_id }}</td><td class="px-4 py-3 font-mono text-xs">{{ task.cron || t('admin.pluginTasks.manualOnly') }}</td><td class="px-4 py-3">{{ task.generation }}</td><td class="px-4 py-3 text-xs">{{ task.next_run_at || '—' }}</td><td class="px-4 py-3">{{ t(task.enabled ? 'admin.pluginInstances.enabled' : 'admin.pluginInstances.disabled') }}</td>
          <td class="px-4 py-3"><div class="flex flex-wrap gap-2">
            <button data-testid="task-details" class="btn btn-secondary btn-sm" :disabled="busy || detailLoading || readDetailLoading" @click="inspectTask(task.id)">{{ t('admin.pluginInstances.details') }}</button>
            <button v-if="can('update')" data-testid="edit-task" class="btn btn-secondary btn-sm" :disabled="busy || detailLoading" @click="editTask(task.id)">{{ t('admin.pluginTasks.edit') }}</button>
            <button v-if="can('run')" data-testid="run-task" class="btn btn-secondary btn-sm" :disabled="busy || detailLoading" @click="runTask(task)">{{ t('admin.pluginTasks.run') }}</button>
            <RouterLink v-if="canRunsRead" :to="{ path: '/admin/plugin-runs', query: { task_id: task.id } }" class="btn btn-secondary btn-sm">{{ t('admin.pluginRuns.title') }}</RouterLink>
            <button v-if="can('delete')" data-testid="delete-task" class="btn btn-danger btn-sm" :disabled="busy || detailLoading" @click="deleting = task">{{ t('admin.pluginTasks.delete') }}</button>
          </div></td>
        </tr></tbody>
      </table>
    </div>
    <p v-if="detailLoading" role="status" class="text-sm text-gray-500">{{ t('admin.pluginInstances.loading') }}</p>
    <BaseDialog :show="!!readDetail && canRead" :title="t('admin.pluginTasks.details')" @close="closeReadDetail">
      <div v-if="readDetail" data-testid="task-read-detail" class="space-y-3">
        <dl class="grid gap-3 sm:grid-cols-2"><div v-for="(value, key) in detailMetadata" :key="key"><dt class="text-xs text-gray-500">{{ t('admin.pluginTasks.fields.' + key) }}</dt><dd class="break-all text-sm">{{ value ?? '—' }}</dd></div></dl>
        <h3 class="text-sm font-medium">{{ t('admin.pluginTasks.scope') }}</h3><pre class="max-h-48 overflow-auto whitespace-pre-wrap break-all rounded-lg bg-gray-50 p-3 text-xs dark:bg-dark-800">{{ JSON.stringify(readDetail.scope, null, 2) }}</pre>
        <h3 class="text-sm font-medium">{{ t('admin.pluginTasks.input') }}</h3><pre class="max-h-48 overflow-auto whitespace-pre-wrap break-all rounded-lg bg-gray-50 p-3 text-xs dark:bg-dark-800">{{ JSON.stringify(readDetail.input, null, 2) }}</pre>
        <p class="text-xs text-gray-500">{{ t('admin.pluginTasks.scopeHint') }}</p>
      </div>
    </BaseDialog>
    <BaseDialog :show="showForm && can(editing ? 'update' : 'create')" :title="t(editing ? 'admin.pluginTasks.edit' : 'admin.pluginTasks.create')" @close="closeForm">
      <form class="space-y-4" @submit.prevent="saveTask">
        <div class="grid gap-3 sm:grid-cols-2">
          <label class="text-sm">{{ t('admin.pluginTasks.instance') }}<input v-model.number="form.instance_id" data-testid="task-instance" class="input mt-1 w-full" type="number" min="1" :disabled="busy || !!instanceId" /></label>
          <label class="text-sm">{{ t('admin.pluginTasks.name') }}<input v-model="form.name" data-testid="task-name" class="input mt-1 w-full" :disabled="busy" /></label>
          <label class="text-sm">{{ t('admin.pluginTasks.entry') }}<input v-model="form.entry" data-testid="task-entry" class="input mt-1 w-full" :disabled="busy" /></label>
          <label class="text-sm">{{ t('admin.pluginTasks.schedule') }}<input v-model="form.cron" data-testid="task-cron" class="input mt-1 w-full font-mono" :disabled="busy" /><span class="mt-1 block text-xs text-gray-500">{{ t('admin.pluginTasks.scheduleHint') }}</span></label>
          <label class="text-sm">{{ t('admin.pluginTasks.timeout') }}<input v-model.number="form.timeout_seconds" data-testid="task-timeout" class="input mt-1 w-full" type="number" min="1" :disabled="busy" /></label>
          <label class="text-sm">{{ t('admin.pluginTasks.retries') }}<input v-model.number="form.max_retries" data-testid="task-retries" class="input mt-1 w-full" type="number" min="0" :disabled="busy" /></label>
          <label class="text-sm">{{ t('admin.pluginTasks.backoff') }}<input v-model.number="form.retry_backoff_seconds" data-testid="task-backoff" class="input mt-1 w-full" type="number" min="0" :disabled="busy" /></label>
          <label class="flex items-center gap-2 text-sm"><input v-model="form.enabled" type="checkbox" :disabled="busy" />{{ t('admin.pluginInstances.enabled') }}</label>
        </div>
        <label class="block text-sm">{{ t('admin.pluginTasks.input') }}<textarea v-model="inputText" data-testid="task-input" class="input mt-1 min-h-24 w-full font-mono text-xs" :disabled="busy" spellcheck="false" /></label>
        <label class="block text-sm">{{ t('admin.pluginTasks.scope') }}<textarea v-model="scopeText" data-testid="task-scope" class="input mt-1 min-h-32 w-full font-mono text-xs" :disabled="busy" spellcheck="false" /></label>
        <p class="text-xs text-gray-500">{{ t('admin.pluginTasks.scopeHint') }}</p>
        <p v-if="formError" role="alert" class="text-sm text-red-600">{{ formError }}</p>
      </form>
      <template #footer><div class="flex justify-end gap-2"><button class="btn btn-secondary" :disabled="busy" @click="closeForm">{{ t('admin.pluginInstances.cancel') }}</button><button data-testid="save-task" class="btn btn-primary" :disabled="busy" @click="saveTask">{{ t('admin.pluginTasks.save') }}</button></div></template>
    </BaseDialog>
    <BaseDialog :show="!!deleting && can('delete')" :title="t('admin.pluginTasks.delete')" @close="closeDelete">
      <p class="text-sm">{{ t('admin.pluginTasks.deleteConfirm', { name: deleting?.name }) }}</p>
      <template #footer><div class="flex justify-end gap-2"><button class="btn btn-secondary" :disabled="busy" @click="closeDelete">{{ t('admin.pluginInstances.cancel') }}</button><button data-testid="confirm-delete-task" class="btn btn-danger" :disabled="busy" @click="deleteTask">{{ t('admin.pluginTasks.delete') }}</button></div></template>
    </BaseDialog>
    <TotpStepUpDialog :controller="stepUp" />
  </section>
</template>
<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import TotpStepUpDialog from '@/components/auth/TotpStepUpDialog.vue'
import { useAuthStore } from '@/stores/auth'
import { pluginTasksAPI, type PluginTask, type CreatePluginTask, type PluginRun, runMetadata } from '@/api/admin/pluginTasks'
import { isStepUpCancelled, isStepUpBlocked, stepUpBlockReason } from '@/composables/useStepUp'
import { usePluginOperation } from './usePluginOperation'
import { emptyScope, objectJSON, taskScope, taskValid } from './taskForm'
const props = defineProps<{ instanceId?: number }>()
const emit = defineEmits<{ run: [taskId: number, runId: number] }>()
const { t } = useI18n(), auth = useAuthStore()
const canRead = computed(() => auth.hasPermission('plugin_tasks.read'))
const canRunsRead = computed(() => auth.hasPermission('plugin_runs.read'))
function can(action: string) { return canRead.value && auth.hasPermission(`plugin_tasks.${action}`) }
const { stepUp, busy, execute, cancel } = usePluginOperation(can)
type Summary = Omit<PluginTask, 'input' | 'scope'>
const acceptedRun = ref<PluginRun | null>(null), readDetail = ref<PluginTask | null>(null), readDetailLoading = ref(false)
const detailMetadata = computed(() => { if (!readDetail.value) return {}; const { input: _input, scope: _scope, ...metadata } = readDetail.value; return metadata })
const tasks = ref<Summary[]>([]), loading = ref(false), detailLoading = ref(false), error = ref(''), filter = ref('')
const editing = ref<{ id: number; generation: number } | null>(null), deleting = ref<Summary | null>(null), showForm = ref(false), formError = ref('')
const defaults = (): CreatePluginTask => ({ instance_id: props.instanceId || Number(filter.value) || 0, name: '', entry: '', cron: '', enabled: true, timeout_seconds: 30, max_retries: 0, retry_backoff_seconds: 5, input: {}, scope: emptyScope() })
const form = ref(defaults()), inputText = ref(''), scopeText = ref('')
let alive = true, readEpoch = 0, formEpoch = 0, inspectEpoch = 0
function current(epoch: number) { return alive && canRead.value && epoch === readEpoch }
function feedback(e: unknown) {
  if (isStepUpBlocked(e)) return t(stepUpBlockReason(e) === 'STEP_UP_ADMIN_API_KEY_FORBIDDEN' ? 'stepUp.adminApiKeyForbidden' : 'stepUp.notEnabled')
  return t(conflict(e) ? 'admin.pluginTasks.conflict' : 'admin.pluginTasks.requestFailed')
}
function conflict(e: unknown) { const v = e as { status?: number; response?: { status?: number } }; return (v?.status || v?.response?.status) === 409 }
async function load() {
  if (!canRead.value || !alive) return
  const instance = props.instanceId || (filter.value ? Number(filter.value) : undefined)
  if (instance !== undefined && (!Number.isSafeInteger(instance) || instance <= 0)) { error.value = t('admin.pluginTasks.invalid'); return }
  const epoch = ++readEpoch; loading.value = true; error.value = ''
  try {
    const values = await pluginTasksAPI.list(instance)
    if (current(epoch)) tasks.value = values.map(({ input: _input, scope: _scope, ...summary }) => summary)
  } catch (e) { if (current(epoch)) error.value = feedback(e) }
  finally { if (epoch === readEpoch) loading.value = false }
}
function closeReadDetail() { inspectEpoch++; readDetail.value = null; readDetailLoading.value = false }
async function inspectTask(id: number) {
  if (!canRead.value || !alive || busy.value) return
  closeReadDetail(); const token = inspectEpoch; readDetailLoading.value = true
  try { const task = await pluginTasksAPI.get(id); if (alive && canRead.value && token === inspectEpoch) readDetail.value = task }
  catch (e) { if (alive && canRead.value && token === inspectEpoch) error.value = feedback(e) }
  finally { if (token === inspectEpoch) readDetailLoading.value = false }
}
function closeForm() { formEpoch++; cancel(); showForm.value = false; editing.value = null; form.value = defaults(); inputText.value = ''; scopeText.value = ''; formError.value = ''; detailLoading.value = false }
function closeDelete() { cancel(); deleting.value = null }
function createForm() { if (!can('create') || busy.value) return; closeForm(); form.value = defaults(); inputText.value = '{}'; scopeText.value = JSON.stringify(emptyScope(), null, 2); showForm.value = true }
async function editTask(id: number) {
  if (!can('update') || busy.value) return
  closeForm(); const epoch = formEpoch; detailLoading.value = true; error.value = ''
  try {
    const task = await pluginTasksAPI.get(id)
    if (!alive || !can('update') || epoch !== formEpoch) return
    editing.value = { id: task.id, generation: task.generation }
    form.value = { instance_id: task.instance_id, name: task.name, entry: task.entry, cron: task.cron, enabled: task.enabled, timeout_seconds: task.timeout_seconds, max_retries: task.max_retries, retry_backoff_seconds: task.retry_backoff_seconds, input: {}, scope: emptyScope() }
    inputText.value = JSON.stringify(task.input, null, 2); scopeText.value = JSON.stringify(task.scope, null, 2); showForm.value = true
  } catch (e) { if (alive && can('update') && epoch === formEpoch) error.value = feedback(e) }
  finally { if (epoch === formEpoch) detailLoading.value = false }
}
async function failure(e: unknown) {
  if (isStepUpCancelled(e) || !alive || !canRead.value) return
  if (conflict(e)) { closeForm(); closeDelete(); await load() }
  if (alive && canRead.value) { error.value = feedback(e); if (showForm.value) formError.value = feedback(e) }
}
async function saveTask() {
  const action = editing.value ? 'update' : 'create'
  if (!can(action) || busy.value || !showForm.value) return
  const input = objectJSON(inputText.value), scope = taskScope(scopeText.value)
  if (!input || !scope || !taskValid(form.value)) { formError.value = t('admin.pluginTasks.invalid'); return }
  const body: CreatePluginTask = { ...form.value, cron: form.value.cron.trim(), input, scope }, edit = editing.value ? { ...editing.value } : null, epoch = formEpoch
  formError.value = ''; error.value = ''
  try {
    await execute(action, () => edit ? pluginTasksAPI.update(edit.id, { ...body, expected_generation: edit.generation }) : pluginTasksAPI.create(body), () => epoch === formEpoch && showForm.value)
    closeForm(); await load()
  } catch (e) { await failure(e) }
}
async function deleteTask() {
  if (!deleting.value || !can('delete') || busy.value) return
  const task = { ...deleting.value }; error.value = ''
  try { await execute('delete', () => pluginTasksAPI.delete(task.id, task.generation), () => deleting.value?.id === task.id); closeDelete(); await load() }
  catch (e) { await failure(e) }
}
async function runTask(task: Summary) {
  if (!can('run') || busy.value) return
  // Generate once per user action; the OTP retry reuses this exact key.
  const key = crypto.randomUUID(); error.value = ''; acceptedRun.value = null
  try { const run = await execute('run', () => pluginTasksAPI.run(task.id, key)); acceptedRun.value = runMetadata(run); emit('run', task.id, run.id); await load() }
  catch (e) { await failure(e) }
}
function changeFilter() { readEpoch++; tasks.value = []; acceptedRun.value = null; closeReadDetail(); closeForm(); closeDelete(); error.value = ''; void load() }
watch(canRead, allowed => { if (!allowed) { readEpoch++; tasks.value = []; loading.value = false; filter.value = ''; error.value = ''; acceptedRun.value = null; closeReadDetail(); closeForm(); closeDelete() } else void load() }, { flush: 'sync' })
watch(() => can('update'), allowed => { if (!allowed && (editing.value || detailLoading.value)) closeForm() }, { flush: 'sync' })
watch(() => can('create'), allowed => { if (!allowed && !editing.value) closeForm() }, { flush: 'sync' })
watch(() => can('run'), allowed => { if (!allowed) acceptedRun.value = null }, { flush: 'sync' })
watch(() => can('delete'), allowed => { if (!allowed) closeDelete() }, { flush: 'sync' })
watch(() => props.instanceId, () => { changeFilter() }, { flush: 'sync' })
onMounted(load)
onBeforeUnmount(() => { alive = false; readEpoch++; closeReadDetail(); acceptedRun.value = null; closeForm(); closeDelete(); tasks.value = [] })
</script>
