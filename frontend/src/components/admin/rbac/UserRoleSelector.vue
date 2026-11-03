<template>
  <section data-test="user-role-selector" class="space-y-3" @input.stop>
    <p class="input-label">{{ rt('rbac.roles') }}</p>
    <p v-if="protectedTarget" role="status" class="text-sm text-amber-700 dark:text-amber-400">{{ rt('rbac.protectedUser') }}</p>
    <p v-else-if="overCeiling" role="status" class="text-sm text-amber-700 dark:text-amber-400">{{ rt('rbac.ceiling') }}</p>
    <p v-else class="input-hint">{{ editable ? rt('rbac.selection') : rt('rbac.readOnly') }}</p>
    <p v-if="loading" role="status" class="input-hint">{{ rt('rbac.loading') }}</p>
    <div v-else-if="error" role="alert" class="space-y-2 text-sm text-red-600 dark:text-red-400">
      <p>{{ error }}</p>
      <button type="button" data-test="retry-user-roles" class="btn btn-secondary btn-sm" :disabled="disabled" @click="loadRoles">{{ rt('rbac.retry') }}</button>
    </div>
    <template v-else-if="loaded">
      <input v-model="query" type="search" class="input" :aria-label="rt('rbac.searchRoles')" :placeholder="rt('rbac.searchRoles')" :disabled="disabled || saving" />
      <div class="max-h-64 space-y-2 overflow-y-auto rounded-xl border border-gray-200 p-3 dark:border-dark-600">
        <label v-for="role in filteredRoles" :key="role.id" class="flex items-start gap-3 rounded-lg p-2 text-sm" :class="{ 'opacity-60': !withinCeiling(auth, role) }">
          <input v-model="selected" type="checkbox" :value="role.id" :data-test="`user-role-${role.id}`" :disabled="disabled || saving || !editable || !withinCeiling(auth, role)" class="mt-1 rounded border-gray-300 text-primary-600" />
          <span class="min-w-0"><span class="block font-medium text-gray-800 dark:text-gray-200">{{ roleName(role) }}</span><span class="block text-xs text-gray-500">{{ role.code }}</span><span v-if="roleDescription(role)" class="block text-gray-500 dark:text-dark-400">{{ roleDescription(role) }}</span></span>
        </label>
        <p v-if="!filteredRoles.length" class="input-hint">{{ roles.length ? rt('rbac.noResults') : rt('rbac.none') }}</p>
      </div>
      <p v-if="editable && !selected.length" class="text-sm text-amber-700 dark:text-amber-400">{{ rt('rbac.emptyRoles') }}</p>
    </template>
  </section>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { rbacAPI, type RBACRole } from '@/api/admin/rbac'
import type { AdminUser } from '@/types'
import { useAuthStore } from '@/stores/auth'
import { useAppStore } from '@/stores/app'
import { isProtectedUser, isSuperRole, withinCeiling, rbacError } from './policy'
import { rbacMessages } from './messages'
import { useRBACLabels } from './labels'

const props = withDefaults(defineProps<{ show: boolean; user: AdminUser; disabled?: boolean }>(), { disabled: false })
const { t: rt } = useI18n({ useScope: 'local', messages: rbacMessages })
const { roleName, roleDescription } = useRBACLabels()
const auth = useAuthStore(), app = useAppStore()
const roles = ref<RBACRole[]>([]), current = ref<RBACRole[]>([])
const selected = ref<number[]>([]), original = ref<number[]>([])
const loading = ref(false), loaded = ref(false), saving = ref(false), error = ref(''), query = ref('')
let generation = 0
const protectedTarget = computed(() => isProtectedUser(props.user) || current.value.some(isSuperRole))
const overCeiling = computed(() => current.value.some(role => !withinCeiling(auth, role)))
const editable = computed(() => loaded.value && auth.hasPermission('users.read') && auth.hasPermission('roles.read') && auth.hasPermission('users.roles.assign') && !protectedTarget.value && !overCeiling.value)
const normalized = (ids: number[]) => [...new Set(ids)].sort((a, b) => a - b)
const dirty = computed(() => loaded.value && JSON.stringify(normalized(selected.value)) !== JSON.stringify(normalized(original.value)))
const filteredRoles = computed(() => {
  const q = query.value.trim().toLowerCase()
  return roles.value.filter(role => `${role.name} ${role.code} ${role.description} ${roleName(role)} ${roleDescription(role)}`.toLowerCase().includes(q))
})

async function loadRoles() {
  const seq = ++generation
  roles.value = []; current.value = []; selected.value = []; original.value = []
  loaded.value = false; loading.value = false; saving.value = false; error.value = ''; query.value = ''
  if (!props.show || !auth.hasPermission('users.read')) return
  loading.value = true
  const userId = props.user.id
  try {
    const [available, assigned] = await Promise.all([
      auth.hasPermission('roles.read') ? rbacAPI.listRoles() : Promise.resolve([]),
      rbacAPI.getUserRoles(userId)
    ])
    if (seq !== generation) return
    current.value = assigned ?? []
    roles.value = [...new Map([...(available ?? []), ...current.value].map(role => [role.id, role])).values()]
    original.value = current.value.map(role => role.id); selected.value = [...original.value]
    loaded.value = true
  } catch (e) {
    if (seq === generation) error.value = rbacError(e, rt('rbac.loadFailed'))
  } finally {
    if (seq === generation) loading.value = false
  }
}
watch([
  () => props.show, () => props.user.id, () => auth.user?.id,
  () => auth.hasPermission('users.read'), () => auth.hasPermission('roles.read')
], loadRoles, { immediate: true })
onBeforeUnmount(() => { generation++ })

function validate() {
  if (!dirty.value) return
  if (!editable.value || selected.value.some(id => {
    const role = roles.value.find(candidate => candidate.id === id)
    return !role || !withinCeiling(auth, role)
  })) throw new Error(rt('rbac.noPermission'))
}
async function save() {
  if (!dirty.value) return
  validate()
  if (saving.value) throw new Error(rt('rbac.saving'))
  const seq = generation, userId = props.user.id, roleIds = normalized(selected.value)
  saving.value = true
  try {
    await rbacAPI.setUserRoles(userId, roleIds)
    if (seq !== generation) return
    original.value = [...roleIds]
    current.value = roles.value.filter(role => roleIds.includes(role.id))
    if (userId === auth.user?.id) {
      try { await auth.refreshUser() } catch { app.showError(rt('rbac.refreshFailed')) }
    }
  } finally {
    if (seq === generation) saving.value = false
  }
}
defineExpose({ validate, save, dirty })
</script>
