<template>
  <BaseDialog :show="show" :title="`${rt('rbac.userRoles')} · ${user.email}`" width="wide" :show-close-button="!saving" :close-on-escape="!saving" @close="close">
    <p v-if="!auth.hasPermission('users.read')" role="status">{{ rt('rbac.noPermission') }}</p>
    <p v-else-if="loading" role="status">{{ rt('rbac.loading') }}</p>
    <div v-else class="space-y-4">
      <p v-if="protectedUser" class="text-sm text-amber-700 dark:text-amber-400">{{ rt('rbac.protectedUser') }}</p>
      <p v-else-if="overCeiling" class="text-sm text-amber-700 dark:text-amber-400">{{ rt('rbac.ceiling') }}</p>
      <p v-else class="text-sm text-gray-500 dark:text-dark-400">{{ editable ? rt('rbac.selection') : rt('rbac.readOnly') }}</p>
      <input v-model="query" type="search" class="input" :aria-label="rt('rbac.searchRoles')" :placeholder="rt('rbac.searchRoles')" />
      <div class="max-h-80 space-y-2 overflow-y-auto">
        <label v-for="role in filtered" :key="role.id" class="flex items-start gap-3 rounded-xl border border-gray-200 p-3 dark:border-dark-600" :class="{ 'opacity-60': !withinCeiling(auth, role) }">
          <input v-model="selected" type="checkbox" class="mt-1 rounded border-gray-300 text-primary-600" :value="role.id" :disabled="!editable || !loaded || saving || !withinCeiling(auth, role)" />
          <span class="min-w-0 text-sm"><span class="block font-medium text-gray-800 dark:text-gray-200">{{ roleName(role) }} <span class="font-normal text-gray-500">{{ role.code }}</span></span><span class="block text-gray-500 dark:text-dark-400">{{ roleDescription(role) }}</span><span v-if="!withinCeiling(auth, role)" class="text-amber-700 dark:text-amber-400">{{ rt('rbac.ceiling') }}</span></span>
        </label>
        <p v-if="!filtered.length" class="text-sm text-gray-500">{{ rt('rbac.noResults') }}</p>
      </div>
      <p v-if="loaded && !selected.length" class="text-sm text-amber-700 dark:text-amber-400">{{ rt('rbac.emptyRoles') }}</p>
    </div>
    <p v-if="error" role="alert" class="mt-3 text-sm text-red-600 dark:text-red-400">{{ error }}</p>
    <button v-if="auth.hasPermission('users.read') && !loaded && !loading" class="btn btn-secondary mt-3" @click="load">{{ rt('rbac.retry') }}</button>
    <template #footer><div class="flex justify-end gap-3">
      <button class="btn btn-secondary" data-testid="cancel" :disabled="saving" @click="close">{{ rt('rbac.cancel') }}</button>
      <button v-if="editable" class="btn btn-primary" data-testid="save-user-roles" :disabled="!loaded || loading || saving" @click="save">{{ saving ? rt('rbac.saving') : rt('rbac.save') }}</button>
    </div></template>
  </BaseDialog>
</template>
<script setup lang="ts">
import { computed, ref, watch, onBeforeUnmount } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import { useAuthStore } from '@/stores/auth'
import { useAppStore } from '@/stores/app'
import { rbacAPI, type RBACRole } from '@/api/admin/rbac'
import type { RoleSummary } from '@/types'
import { isProtectedUser, isSuperRole, withinCeiling, rbacError } from './policy'
import { rbacMessages } from './messages'
import { useRBACLabels } from './labels'
const props = defineProps<{ show: boolean; user: { id: number; email: string; role?: string; is_super_admin?: boolean; roles?: RoleSummary[] } }>()
const emit = defineEmits<{ close: []; saved: [] }>()
const { t: rt } = useI18n({ useScope: 'local', messages: rbacMessages })
const { roleName, roleDescription } = useRBACLabels()
const auth = useAuthStore(), app = useAppStore()
const roles = ref<RBACRole[]>([]), current = ref<RBACRole[]>([]), selected = ref<number[]>([])
const loading = ref(false), loaded = ref(false), saving = ref(false), error = ref(''), query = ref('')
let generation = 0
const protectedUser = computed(() => isProtectedUser(props.user) || current.value.some(isSuperRole))
const overCeiling = computed(() => current.value.some(role => !withinCeiling(auth, role)))
const editable = computed(() => auth.hasPermission('users.roles.assign') && auth.hasPermission('roles.read') && auth.hasPermission('users.read') && !protectedUser.value && !overCeiling.value)
const filtered = computed(() => roles.value.filter(role => `${role.name} ${role.code} ${role.description} ${roleName(role)} ${roleDescription(role)}`.toLowerCase().includes(query.value.trim().toLowerCase())))
async function load() {
  const seq = ++generation
  loaded.value = false; loading.value = false; error.value = ''; roles.value = []; current.value = []; selected.value = []; query.value = ''
  if (!props.show || !auth.hasPermission('users.read')) return
  loading.value = true
  try {
    const [available, assigned] = await Promise.all([
      auth.hasPermission('roles.read') ? rbacAPI.listRoles() : Promise.resolve([]), rbacAPI.getUserRoles(props.user.id)
    ])
    if (seq !== generation) return
    current.value = assigned ?? []
    roles.value = [...new Map([...available, ...current.value].map(role => [role.id, role])).values()]
    selected.value = current.value.map(role => role.id); loaded.value = true
  } catch (e) { if (seq === generation) error.value = rbacError(e, rt('rbac.loadFailed')) }
  finally { if (seq === generation) loading.value = false }
}
// Invalidate reads when their authority changes, without resetting drafts on
// an equivalent refresh of the current user's permission snapshot.
watch([
  () => props.show,
  () => props.user.id,
  () => auth.user?.id,
  () => auth.hasPermission('users.read'),
  () => auth.hasPermission('roles.read')
], load, { immediate: true })
onBeforeUnmount(() => { generation++ })
function close() { if (!saving.value) emit('close') }
async function save() {
  if (!editable.value || !loaded.value || saving.value) return
  if (selected.value.some(id => { const role = roles.value.find(r => r.id === id); return !role || !withinCeiling(auth, role) })) return
  saving.value = true; error.value = ''
  try {
    await rbacAPI.setUserRoles(props.user.id, [...selected.value])
    if (props.user.id === auth.user?.id) {
      try { await auth.refreshUser() } catch { app.showError(rt('rbac.refreshFailed')) }
    }
    app.showSuccess(rt('rbac.saved')); emit('saved'); emit('close')
  } catch (e) { error.value = rbacError(e, rt('rbac.saveFailed')) }
  finally { saving.value = false }
}
</script>
