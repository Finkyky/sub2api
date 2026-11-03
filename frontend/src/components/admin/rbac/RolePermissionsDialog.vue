<template>
  <BaseDialog :show="show" :title="`${rt('rbac.permissions')} · ${roleName(role)}`" width="wide" :show-close-button="!saving" :close-on-escape="!saving" @close="close">
    <p v-if="!auth.hasPermission('roles.read')" role="status">{{ rt('rbac.noPermission') }}</p>
    <p v-else-if="loading" role="status">{{ rt('rbac.loading') }}</p>
    <div v-else class="space-y-4">
      <p v-if="isProtectedRole(role)" class="text-sm text-amber-700 dark:text-amber-400">{{ rt('rbac.lockedRole') }}</p>
      <p v-else-if="!withinCeiling(auth, effectiveRole)" class="text-sm text-amber-700 dark:text-amber-400">{{ rt('rbac.ceiling') }}</p>
      <PermissionPicker :model-value="selected" :permissions="catalog" :disabled="!editable || !loaded || saving" @update:model-value="selected = $event" />
      <p v-if="loaded && !selected.length" class="text-sm text-amber-700 dark:text-amber-400">{{ rt('rbac.emptyPermissions') }}</p>
    </div>
    <p v-if="error" role="alert" class="mt-3 text-sm text-red-600 dark:text-red-400">{{ error }}</p>
    <button v-if="auth.hasPermission('roles.read') && !loaded && !loading" class="btn btn-secondary mt-3" @click="load">{{ rt('rbac.retry') }}</button>
    <template #footer><div class="flex justify-end gap-3">
      <button class="btn btn-secondary" data-testid="cancel" :disabled="saving" @click="close">{{ rt('rbac.cancel') }}</button>
      <button v-if="editable" class="btn btn-primary" data-testid="save-permissions" :disabled="loading || !loaded || saving" @click="save">{{ saving ? rt('rbac.saving') : rt('rbac.save') }}</button>
    </div></template>
  </BaseDialog>
</template>
<script setup lang="ts">
import { computed, ref, watch, onBeforeUnmount } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import PermissionPicker from './PermissionPicker.vue'
import { useAuthStore } from '@/stores/auth'
import { useAppStore } from '@/stores/app'
import { rbacAPI, type RBACRole, type RBACPermission } from '@/api/admin/rbac'
import { canManageRole, withinCeiling, isProtectedRole, rbacError } from './policy'
import { rbacMessages } from './messages'
import { useRBACLabels } from './labels'
const props = defineProps<{ show: boolean; role: RBACRole }>()
const emit = defineEmits<{ close: []; saved: [] }>()
const { t: rt } = useI18n({ useScope: 'local', messages: rbacMessages })
const { roleName } = useRBACLabels()
const auth = useAuthStore(), app = useAppStore()
const selected = ref<string[]>([]), original = ref<string[]>([]), catalog = ref<RBACPermission[]>([])
const loading = ref(false), loaded = ref(false), saving = ref(false), error = ref('')
let generation = 0
const effectiveRole = computed(() => ({ ...props.role, permissions: loaded.value ? original.value : props.role.permissions }))
const editable = computed(() => auth.hasPermission('roles.read') && auth.hasPermission('roles.permissions.assign') && auth.hasPermission('permissions.read') && canManageRole(auth, effectiveRole.value))
async function load() {
  const seq = ++generation
  loaded.value = false; loading.value = false; error.value = ''; selected.value = []; original.value = []; catalog.value = []
  if (!props.show || !auth.hasPermission('roles.read')) return
  loading.value = true
  try {
    const [codes, definitions] = await Promise.all([
      rbacAPI.getRolePermissions(props.role.id),
      auth.hasPermission('permissions.read') ? rbacAPI.listPermissions() : Promise.resolve([])
    ])
    if (seq !== generation) return
    original.value = codes ?? []; selected.value = [...original.value]
    // Role readers without catalog access can still inspect assigned permission codes.
    catalog.value = definitions.length ? definitions : original.value.map(code => ({ code, resource: code.split('.')[0], action: '', name: code, description: '' }))
    loaded.value = true
  } catch (e) { if (seq === generation) error.value = rbacError(e, rt('rbac.loadFailed')) }
  finally { if (seq === generation) loading.value = false }
}
// Invalidate reads when their authority changes, without resetting drafts on
// an equivalent refresh of the current user's permission snapshot.
watch([
  () => props.show,
  () => props.role.id,
  () => auth.user?.id,
  () => auth.hasPermission('roles.read'),
  () => auth.hasPermission('permissions.read')
], load, { immediate: true })
onBeforeUnmount(() => { generation++ })
function close() { if (!saving.value) emit('close') }
async function save() {
  if (!editable.value || !loaded.value || saving.value || selected.value.some(code => !auth.hasPermission(code))) return
  saving.value = true; error.value = ''
  try {
    await rbacAPI.setRolePermissions(props.role.id, [...selected.value])
    try { await auth.refreshUser() } catch { app.showError(rt('rbac.refreshFailed')) }
    app.showSuccess(rt('rbac.saved')); emit('saved'); emit('close')
  } catch (e) { error.value = rbacError(e, rt('rbac.saveFailed')) }
  finally { saving.value = false }
}
</script>
