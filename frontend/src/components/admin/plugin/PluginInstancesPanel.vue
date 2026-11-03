<template>
    <div class="space-y-6">
      <section class="flex flex-col gap-4 border-b border-gray-200 pb-5 dark:border-dark-700 sm:flex-row sm:items-end sm:justify-between">
        <div><h1 class="text-2xl font-bold text-gray-900 dark:text-white">{{ t('admin.pluginInstances.title') }}</h1><p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ t('admin.pluginInstances.description') }}</p></div>
        <div class="flex gap-2">
          <button v-if="canRead" class="btn btn-secondary" :disabled="loading || saving" @click="load">{{ t('admin.pluginInstances.refresh') }}</button>
          <button v-if="can('create')" data-testid="create-instance" class="btn btn-primary" :disabled="writeLocked" @click="showCreate = true">{{ t('admin.pluginInstances.create') }}</button>
        </div>
      </section>
      <div v-if="error" class="rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700 dark:border-red-900/50 dark:bg-red-950/20 dark:text-red-300" role="alert">{{ error }}</div>
      <p v-if="!canRead" class="py-12 text-center text-sm text-gray-500">{{ t('admin.pluginInstances.readDenied') }}</p>
      <div v-else-if="loading" class="py-12 text-center text-sm text-gray-500" role="status">{{ t('admin.pluginInstances.loading') }}</div>
      <div v-else-if="instances.length === 0" class="rounded-lg border border-dashed border-gray-300 p-12 text-center text-sm text-gray-500 dark:border-dark-600">{{ t('admin.pluginInstances.empty') }}</div>
      <div v-else class="overflow-x-auto rounded-lg border border-gray-200 dark:border-dark-700">
        <table class="min-w-full divide-y divide-gray-200 text-sm dark:divide-dark-700">
          <thead class="bg-gray-50 dark:bg-dark-800"><tr><th class="px-4 py-3 text-left">{{ t('admin.pluginInstances.name') }}</th><th class="px-4 py-3 text-left">{{ t('admin.pluginInstances.plugin') }}</th><th class="px-4 py-3 text-left">{{ t('admin.pluginInstances.generation') }}</th><th class="px-4 py-3 text-left">{{ t('admin.pluginInstances.revision') }}</th><th class="px-4 py-3 text-left">{{ t('admin.pluginInstances.state') }}</th><th class="px-4 py-3 text-right">{{ t('admin.pluginInstances.actions') }}</th></tr></thead>
          <tbody class="divide-y divide-gray-200 bg-white dark:divide-dark-700 dark:bg-dark-900">
            <tr v-for="item in instances" :key="item.id">
              <td class="px-4 py-3 font-medium text-gray-900 dark:text-white">{{ item.name }}</td><td class="px-4 py-3">#{{ item.plugin_id }}</td><td class="px-4 py-3 font-mono text-xs">{{ item.generation }}</td><td class="px-4 py-3 font-mono text-xs">{{ item.current_revision }} / {{ item.latest_revision }}</td>
              <td class="px-4 py-3"><span class="rounded px-2 py-1 text-xs" :class="item.enabled ? 'bg-emerald-100 text-emerald-700' : 'bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-gray-300'">{{ item.enabled ? t('admin.pluginInstances.enabled') : t('admin.pluginInstances.disabled') }}</span></td>
              <td class="px-4 py-3"><div class="flex justify-end gap-2"><RouterLink v-if="auth.hasPermission('plugin_tasks.read')" :to="{ path: '/admin/plugin-tasks', query: { instance_id: item.id } }" class="btn btn-secondary btn-sm">{{ t('admin.pluginTasks.title') }}</RouterLink><button data-testid="instance-details" class="btn btn-secondary btn-sm" :disabled="saving || detailLoading" @click="openItem(item.id)">{{ t('admin.pluginInstances.details') }}</button><button v-if="item.enabled && can('disable')" data-testid="disable-instance" class="btn btn-secondary btn-sm" :disabled="writeLocked" @click="disableItem(item)">{{ t('admin.pluginInstances.disable') }}</button></div></td>
            </tr>
          </tbody>
        </table>
      </div>
      <p v-if="detailLoading" class="text-sm text-gray-500" role="status">{{ t('admin.pluginInstances.loading') }}</p>

      <BaseDialog :show="showCreate && can('create')" :title="t('admin.pluginInstances.create')" @close="closeCreate">
        <div class="space-y-4">
          <label class="block text-sm">{{ t('admin.pluginInstances.pluginId') }}<input v-model.number="createForm.plugin_id" data-testid="create-plugin-id" type="number" min="1" step="1" :disabled="writeLocked" class="input mt-1 w-full" /></label>
          <label class="block text-sm">{{ t('admin.pluginInstances.name') }}<input v-model="createForm.name" data-testid="create-name" :disabled="writeLocked" class="input mt-1 w-full" /></label>
          <PluginConfigEditor v-model="createForm.config" data-testid="create-config" :disabled="writeLocked" />
          <p v-if="createError" class="text-sm text-red-600" role="alert">{{ createError }}</p>
        </div>
        <template #footer><div class="flex justify-end gap-2"><button class="btn btn-secondary" @click="closeCreate">{{ t('admin.pluginInstances.cancel') }}</button><button data-testid="submit-create" class="btn btn-primary" :disabled="writeLocked" @click="createItem">{{ t('admin.pluginInstances.create') }}</button></div></template>
      </BaseDialog>

      <BaseDialog :show="!!selected && canRead" :title="selected ? t('admin.pluginInstances.instanceTitle', { name: selected.instance.name }) : ''" width="wide" @close="closeDetail">
        <div v-if="selected && canRead" class="space-y-5">
          <div class="flex items-start justify-between gap-3">
            <dl class="grid flex-1 grid-cols-2 gap-3 text-sm md:grid-cols-4"><div><dt class="text-xs text-gray-500">{{ t('admin.pluginInstances.plugin') }}</dt><dd>#{{ selected.instance.plugin_id }}</dd></div><div><dt class="text-xs text-gray-500">{{ t('admin.pluginInstances.generation') }}</dt><dd>{{ selected.instance.generation }}</dd></div><div><dt class="text-xs text-gray-500">{{ t('admin.pluginInstances.currentRevision') }}</dt><dd>{{ selected.instance.current_revision }}</dd></div><div><dt class="text-xs text-gray-500">{{ t('admin.pluginInstances.bindings') }}</dt><dd>{{ selected.bindings.length }}</dd></div></dl>
            <button data-testid="refresh-detail" class="btn btn-secondary btn-sm" :disabled="saving || detailLoading" @click="openItem(selected.instance.id)">{{ t('admin.pluginInstances.refresh') }}</button>
          </div>
          <p class="text-xs text-gray-500">{{ t('admin.pluginInstances.refreshHint') }}</p>
          <p v-if="formError" class="rounded-lg border border-red-200 px-3 py-2 text-sm text-red-600" role="alert">{{ formError }}</p>
          <PluginInstanceStatusPanel :status="runtimeStatus" :loading="statusLoading" :error="statusError" />
          <section v-if="can('update')" class="space-y-3 border-t border-gray-200 pt-4 dark:border-dark-700">
            <h3 class="text-sm font-semibold">{{ t('admin.pluginInstances.newRevision') }}</h3>
            <PluginConfigEditor v-model="configText" :disabled="writeLocked" />
            <button data-testid="save-config" class="btn btn-secondary btn-sm" :disabled="writeLocked" @click="saveConfig">{{ t('admin.pluginInstances.saveConfig') }}</button>
          </section>
          <PluginBindingsEditor v-model="bindingsDraft" :instance-id="selected.instance.id" :hooks="hooks" :groups="groups" :readonly="!can('bindings.update')" :disabled="writeLocked" />
          <p v-if="groupsError" class="text-xs text-red-600" role="alert">{{ groupsError }}</p>
          <section class="rounded-lg bg-gray-50 p-3 text-xs dark:bg-dark-800">
            <h3 class="mb-2 font-semibold">{{ t('admin.pluginInstances.revisions') }}</h3>
            <p v-if="revisionsError" role="alert" class="text-red-600">{{ revisionsError }}</p>
            <div v-for="revision in revisions" :key="revision.revision" class="flex items-center justify-between border-b border-gray-200 py-2 last:border-0 dark:border-dark-700"><span>{{ t('admin.pluginInstances.revisionSummary', { revision: revision.revision, version: revision.config_version }) }}</span><button v-if="can('publish')" data-testid="publish-revision" class="btn btn-secondary btn-sm" :disabled="writeLocked || (selected.instance.enabled && revision.revision === selected.instance.current_revision)" @click="publishItem(revision.revision)">{{ selected.instance.enabled && revision.revision === selected.instance.current_revision ? t('admin.pluginInstances.current') : t('admin.pluginInstances.publish') }}</button></div>
          </section>
        </div>
        <template #footer><div v-if="selected && canRead" class="flex flex-wrap justify-end gap-2">
          <button v-if="can('bindings.update')" data-testid="save-bindings" class="btn btn-secondary" :disabled="writeLocked" @click="saveBindings">{{ t('admin.pluginInstances.saveBindings') }}</button>
          <button v-if="can('disable') && selected.instance.enabled" class="btn btn-secondary" :disabled="writeLocked" @click="disableItem(selected.instance)">{{ t('admin.pluginInstances.disable') }}</button>
          <button v-if="can('delete')" data-testid="delete-instance" class="btn btn-danger" :disabled="writeLocked || selected.instance.enabled" :title="t('admin.pluginInstances.deleteHint')" @click="showDelete = true">{{ t('admin.pluginInstances.delete') }}</button>
          <button data-testid="close-detail" class="btn btn-secondary" @click="closeDetail">{{ t('admin.pluginInstances.close') }}</button>
        </div></template>
      </BaseDialog>
      <BaseDialog :show="showDelete && !!selected && can('delete')" :title="t('admin.pluginInstances.delete')" @close="showDelete = false">
        <p class="text-sm">{{ t('admin.pluginInstances.deleteConfirm', { name: selected?.instance.name }) }}</p>
        <p v-if="formError" class="mt-3 text-sm text-red-600" role="alert">{{ formError }}</p>
        <template #footer><div class="flex justify-end gap-2"><button class="btn btn-secondary" :disabled="writeLocked" @click="showDelete = false">{{ t('admin.pluginInstances.cancel') }}</button><button data-testid="confirm-delete" class="btn btn-danger" :disabled="writeLocked" @click="deleteItem">{{ t('admin.pluginInstances.delete') }}</button></div></template>
      </BaseDialog>
      <TotpStepUpDialog :controller="stepUp" />
    </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink } from 'vue-router'
import BaseDialog from '@/components/common/BaseDialog.vue'
import TotpStepUpDialog from '@/components/auth/TotpStepUpDialog.vue'
import PluginConfigEditor from '@/components/admin/plugin/PluginConfigEditor.vue'
import PluginBindingsEditor from '@/components/admin/plugin/PluginBindingsEditor.vue'
import PluginInstanceStatusPanel from '@/components/admin/plugin/PluginInstanceStatusPanel.vue'
import { bindingsValid, parseConfig } from '@/components/admin/plugin/bindings'
import { adminAPI } from '@/api/admin'
import type { PluginGroupBinding, PluginInstance, PluginInstanceDetail, PluginInstanceRevision, PluginInstanceStatus } from '@/api/admin/pluginInstances'
import { useAuthStore } from '@/stores/auth'
import { useAppStore } from '@/stores/app'
import { useStepUp, StepUpCancelledError, isStepUpCancelled, isStepUpBlocked, stepUpBlockReason } from '@/composables/useStepUp'

const props = defineProps<{ pluginId?: number; initialInstanceId?: number }>()
const emit = defineEmits<{ selected: [id: number] }>()
const auth = useAuthStore(), app = useAppStore(), stepUp = useStepUp()
const { t } = useI18n()
const canRead = computed(() => auth.hasPermission('plugin_instances.read'))
function can(action: string) { return canRead.value && auth.hasPermission(`plugin_instances.${action}`) }
const instances = ref<PluginInstance[]>([]), selected = ref<PluginInstanceDetail | null>(null)
const revisions = ref<PluginInstanceRevision[]>([]), runtimeStatus = ref<PluginInstanceStatus | null>(null)
const groups = ref<{ id: number; name: string }[]>([]), bindingsDraft = ref<PluginGroupBinding[]>([])
const hooks = computed(() => selected.value?.manifest.general?.hooks || [])
const loading = ref(false), detailLoading = ref(false), statusLoading = ref(false), saving = ref(false)
const writeLocked = computed(() => saving.value || detailLoading.value)
const error = ref(''), formError = ref(''), createError = ref(''), statusError = ref(''), revisionsError = ref(''), groupsError = ref('')
const showCreate = ref(false), showDelete = ref(false), configText = ref('')
const createForm = ref({ plugin_id: 0, name: '', config: '' })
let listEpoch = 0, detailEpoch = 0, createEpoch = 0, writeEpoch = 0, alive = true
const activeWriteAction = ref<string | null>(null)
function detailIsCurrent(epoch: number) { return alive && canRead.value && epoch === detailEpoch }
function feedback(value: unknown): string {
  if (isStepUpBlocked(value)) return stepUpBlockReason(value) === 'STEP_UP_ADMIN_API_KEY_FORBIDDEN' ? t('stepUp.adminApiKeyForbidden') : t('stepUp.notEnabled')
  const e = value as { status?: number; response?: { status?: number }; permissionChanged?: boolean }
  if (e?.permissionChanged) return t('admin.pluginInstances.permissionChanged')
  if ((e?.status || e?.response?.status) === 409) return t('admin.pluginInstances.conflict')
  // Never render an arbitrary exception message or server metadata beside config drafts.
  return t('admin.pluginInstances.requestFailed')
}
function closeCreate() { createEpoch++; showCreate.value = false; createForm.value = { plugin_id: 0, name: '', config: '' }; createError.value = '' }
function closeDetail() {
  detailEpoch++; selected.value = null; configText.value = ''; bindingsDraft.value = []; groups.value = []
  revisions.value = []; runtimeStatus.value = null; showDelete.value = false; detailLoading.value = false; statusLoading.value = false
  formError.value = ''; statusError.value = ''; revisionsError.value = ''; groupsError.value = ''
}
async function load() {
  if (!canRead.value || !alive) return
  const epoch = ++listEpoch; loading.value = true; error.value = ''
  try { const list = await adminAPI.pluginInstances.list(props.pluginId); if (alive && canRead.value && epoch === listEpoch) instances.value = list }
  catch (e) { if (alive && canRead.value && epoch === listEpoch) error.value = feedback(e) }
  finally { if (epoch === listEpoch) loading.value = false }
}
function cloneBindings(value: PluginGroupBinding[]) { return value.map(binding => ({ instance_id: binding.instance_id, group_id: binding.group_id, stage: binding.stage, entry: binding.entry, protocol: binding.protocol, sort_order: binding.sort_order, enabled: binding.enabled, can_replace: binding.can_replace })) }
async function readPanels(id: number, epoch: number) {
  statusLoading.value = true; statusError.value = ''; revisionsError.value = ''
  await Promise.all([
    adminAPI.pluginInstances.status(id).then(value => { if (detailIsCurrent(epoch)) runtimeStatus.value = value }).catch(e => { if (detailIsCurrent(epoch)) statusError.value = feedback(e) }).finally(() => { if (detailIsCurrent(epoch)) statusLoading.value = false }),
    adminAPI.pluginInstances.revisions(id).then(value => { if (detailIsCurrent(epoch)) revisions.value = value }).catch(e => { if (detailIsCurrent(epoch)) revisionsError.value = feedback(e) })
  ])
}
async function loadGroups(epoch: number) {
  if (!can('bindings.update') || !auth.hasPermission('groups.read')) return
  try { const list = await adminAPI.groups.getAllIncludingInactive(); if (detailIsCurrent(epoch) && can('bindings.update') && auth.hasPermission('groups.read')) groups.value = list.map(group => ({ id: group.id, name: group.name })) }
  catch { if (detailIsCurrent(epoch) && can('bindings.update') && auth.hasPermission('groups.read')) groupsError.value = t('admin.pluginInstances.groupsFailed') }
}
async function openItem(id: number, internal = false) {
  if (!canRead.value || (saving.value && !internal)) return
  const epoch = ++detailEpoch; detailLoading.value = true; formError.value = ''; error.value = ''; showDelete.value = false
  try {
    const detail = await adminAPI.pluginInstances.get(id)
    if (!detailIsCurrent(epoch)) return
    selected.value = detail; emit('selected', detail.instance.id); configText.value = ''; bindingsDraft.value = cloneBindings(detail.bindings); groups.value = []
    runtimeStatus.value = null; revisions.value = []; groupsError.value = ''
    await Promise.all([readPanels(id, epoch), loadGroups(epoch)])
  } catch (e) { if (detailIsCurrent(epoch)) { if (selected.value) formError.value = feedback(e); else error.value = feedback(e) } }
  finally { if (detailIsCurrent(epoch)) detailLoading.value = false }
}
// A cancelled operation must not enter the step-up classifier when its first
// request rejects late. Cancelling an already-open prompt alone is insufficient.
async function guardedRequest<T>(token: number, isCurrent: () => boolean, request: () => Promise<T>): Promise<T> {
  const valid = () => alive && token === writeEpoch && isCurrent()
  if (!valid()) throw new StepUpCancelledError()
  try { return await request() }
  catch (e) { if (!valid()) throw new StepUpCancelledError(); throw e }
}
async function writeDetail(action: string, item: PluginInstance, call: () => Promise<PluginInstanceDetail>, success: string) {
  if (writeLocked.value || !can(action)) return
  const epoch = detailEpoch, isSelected = selected.value?.instance.id === item.id
  const token = ++writeEpoch; activeWriteAction.value = action
  saving.value = true; formError.value = ''; error.value = ''
  try {
    const result = await stepUp.run(() => {
      if (token !== writeEpoch || !alive || !can(action) || epoch !== detailEpoch || (isSelected && (selected.value?.instance.id !== item.id || selected.value.instance.generation !== item.generation))) throw { permissionChanged: true }
      return guardedRequest(token, () => can(action) && epoch === detailEpoch, call)
    })
    if (token !== writeEpoch || !detailIsCurrent(epoch)) return
    if (isSelected) {
      selected.value = result; bindingsDraft.value = cloneBindings(result.bindings)
      if (action === 'update') configText.value = ''
      await readPanels(item.id, epoch)
    }
    await load(); if (detailIsCurrent(epoch)) app.showSuccess(success)
  } catch (e) { if (!isStepUpCancelled(e) && detailIsCurrent(epoch)) { if (isSelected) formError.value = feedback(e); else error.value = feedback(e) } }
  finally { saving.value = false; activeWriteAction.value = null }
}
async function saveConfig() {
  if (!selected.value || !can('update')) return
  const config = parseConfig(configText.value)
  if (!config) { formError.value = t('admin.pluginInstances.configInvalid'); return }
  const item = { ...selected.value.instance }
  await writeDetail('update', item, () => adminAPI.pluginInstances.saveConfig(item.id, { expected_generation: item.generation, config }), t('admin.pluginInstances.configSaved'))
}
async function saveBindings() {
  if (!selected.value || !can('bindings.update')) return
  if (!bindingsValid(bindingsDraft.value, hooks.value, selected.value.instance.id)) { formError.value = t('admin.pluginInstances.bindingsInvalid'); return }
  const item = { ...selected.value.instance }, bindings = cloneBindings(bindingsDraft.value)
  await writeDetail('bindings.update', item, () => adminAPI.pluginInstances.replaceBindings(item.id, { expected_generation: item.generation, bindings }), t('admin.pluginInstances.bindingsSaved'))
}
async function publishItem(revision: number) {
  if (!selected.value || (selected.value.instance.enabled && revision === selected.value.instance.current_revision)) return
  const item = { ...selected.value.instance }
  await writeDetail('publish', item, () => adminAPI.pluginInstances.publish(item.id, { expected_generation: item.generation, revision }), t('admin.pluginInstances.published'))
}
async function disableItem(item: PluginInstance) {
  const snapshot = { ...item }
  await writeDetail('disable', snapshot, () => adminAPI.pluginInstances.disable(snapshot.id, snapshot.generation), t('admin.pluginInstances.instanceDisabled'))
}
async function createItem() {
  if (writeLocked.value || !can('create')) return
  const config = parseConfig(createForm.value.config), name = createForm.value.name.trim(), plugin_id = createForm.value.plugin_id
  if (!Number.isSafeInteger(plugin_id) || plugin_id <= 0 || !name) { createError.value = t('admin.pluginInstances.createInvalid'); return }
  if (!config) { createError.value = t('admin.pluginInstances.configInvalid'); return }
  const epoch = createEpoch, token = ++writeEpoch; activeWriteAction.value = 'create'; saving.value = true; createError.value = ''
  try {
    const result = await stepUp.run(() => { if (token !== writeEpoch || !alive || !showCreate.value || !can('create') || epoch !== createEpoch) throw { permissionChanged: true }; return guardedRequest(token, () => can('create') && showCreate.value && epoch === createEpoch, () => adminAPI.pluginInstances.create({ plugin_id, name, config })) })
    if (token !== writeEpoch || !alive || !can('create') || epoch !== createEpoch) return
    closeCreate(); const closedEpoch = createEpoch
    app.showSuccess(t('admin.pluginInstances.created')); await load()
    if (token !== writeEpoch || !alive || !can('create') || closedEpoch !== createEpoch) return
    await openItem(result.instance.id, true)
  } catch (e) { if (alive && canRead.value && epoch === createEpoch && !isStepUpCancelled(e)) createError.value = feedback(e) }
  finally { saving.value = false; activeWriteAction.value = null }
}
async function deleteItem() {
  if (!selected.value || !showDelete.value || writeLocked.value || !can('delete') || selected.value.instance.enabled) return
  const item = { ...selected.value.instance }, epoch = detailEpoch, token = ++writeEpoch; activeWriteAction.value = 'delete'; saving.value = true; formError.value = ''
  try {
    const result = await stepUp.run(() => {
      if (token !== writeEpoch || !detailIsCurrent(epoch) || !can('delete') || !showDelete.value || selected.value?.instance.id !== item.id || selected.value.instance.generation !== item.generation) throw { permissionChanged: true }
      return guardedRequest(token, () => can('delete') && detailIsCurrent(epoch) && showDelete.value, () => adminAPI.pluginInstances.delete(item.id, item.generation))
    })
    if (token !== writeEpoch || !detailIsCurrent(epoch)) return
    if (!result.deleted) { formError.value = t('admin.pluginInstances.requestFailed'); return }
    closeDetail(); app.showSuccess(t('admin.pluginInstances.deleted')); await load()
  } catch (e) { if (detailIsCurrent(epoch) && !isStepUpCancelled(e)) formError.value = feedback(e) }
  finally { saving.value = false; activeWriteAction.value = null }
}
// Revocation permanently invalidates the current operation, even if permission
// is granted again before the OTP prompt or a later await finishes.
watch(() => activeWriteAction.value ? can(activeWriteAction.value) : true, allowed => {
  if (!allowed) { writeEpoch++; stepUp.onCancel() }
}, { flush: 'sync' })
watch(canRead, value => {
  if (!value) { listEpoch++; instances.value = []; loading.value = false; error.value = ''; closeDetail(); closeCreate(); stepUp.onCancel() }
  else void load()
}, { flush: 'sync' })
watch(() => can('update'), value => { if (!value) configText.value = '' }, { flush: 'sync' })
watch(() => can('create'), value => { if (!value) closeCreate() }, { flush: 'sync' })
watch(() => can('delete'), value => { if (!value) showDelete.value = false }, { flush: 'sync' })
watch(() => can('bindings.update'), value => { if (!value) { bindingsDraft.value = selected.value ? cloneBindings(selected.value.bindings) : []; groups.value = []; groupsError.value = '' } }, { flush: 'sync' })
watch(() => auth.hasPermission('groups.read'), value => { if (!value) { groups.value = []; groupsError.value = '' } }, { flush: 'sync' })
watch(() => props.pluginId, () => { listEpoch++; closeDetail(); closeCreate(); void load() })
watch(() => props.initialInstanceId, id => { closeDetail(); if (id) void openItem(id) })
onMounted(() => { void load(); if (props.initialInstanceId) void openItem(props.initialInstanceId) })
onBeforeUnmount(() => { alive = false; listEpoch++; closeDetail(); closeCreate(); stepUp.onCancel() })
</script>
