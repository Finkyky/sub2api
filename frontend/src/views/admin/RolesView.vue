<template>
  <AppLayout>
    <TablePageLayout>
      <template #actions>
        <div class="flex flex-wrap items-center justify-between gap-4">
          <div><h1 class="text-2xl font-bold text-gray-900 dark:text-white">{{ rt('rbac.title') }}</h1><p class="mt-1 text-sm text-gray-500 dark:text-dark-400">{{ rt('rbac.subtitle') }}</p></div>
          <button v-if="auth.hasPermission('roles.create') && auth.hasPermission('roles.read')" class="btn btn-primary" data-testid="create-role" @click="editingRole = null; editorOpen = true">{{ rt('rbac.create') }}</button>
        </div>
      </template>
      <template #filters>
        <div class="flex flex-wrap items-center gap-3">
          <button v-if="auth.hasPermission('roles.read')" class="btn" :class="tab === 'roles' ? 'btn-primary' : 'btn-secondary'" :aria-pressed="tab === 'roles'" @click="tab = 'roles'">{{ rt('rbac.roles') }}</button>
          <button v-if="auth.hasPermission('permissions.read')" class="btn" :class="tab === 'permissions' ? 'btn-primary' : 'btn-secondary'" :aria-pressed="tab === 'permissions'" @click="tab = 'permissions'">{{ rt('rbac.catalog') }}</button>
          <input v-if="tab === 'roles' && auth.hasPermission('roles.read')" v-model="search" type="search" class="input sm:max-w-xs" :placeholder="rt('rbac.searchRoles')" :aria-label="rt('rbac.searchRoles')" />
        </div>
      </template>
      <template #table>
        <div v-if="!auth.hasPermission('roles.read') && !auth.hasPermission('permissions.read')" class="p-6" role="status">{{ rt('rbac.noPermission') }}</div>
        <template v-else-if="tab === 'roles' && auth.hasPermission('roles.read')">
          <div v-if="roleError" role="alert" class="p-4 text-sm text-red-600 dark:text-red-400">{{ roleError }} <button class="btn btn-secondary ml-3" data-testid="retry-roles" @click="loadRoles">{{ rt('rbac.retry') }}</button></div>
          <DataTable :columns="columns" :data="filteredRoles" :loading="loadingRoles" row-key="id">
            <template #cell-name="{ row }"><div class="py-1"><p class="font-medium text-gray-900 dark:text-white">{{ roleName(row) }}</p><p class="text-xs text-gray-500">{{ row.code }}</p><p v-if="isProtectedRole(row)" class="mt-1 max-w-xs text-xs text-amber-700 dark:text-amber-400">{{ rt('rbac.lockedRole') }}</p><p v-else-if="!withinCeiling(auth, row)" class="mt-1 max-w-xs text-xs text-amber-700 dark:text-amber-400">{{ rt('rbac.ceiling') }}</p></div></template>
            <template #cell-description="{ row }">{{ roleDescription(row) }}</template>
            <template #cell-permissions="{ row }"><span class="badge badge-gray">{{ row.permissions?.length ?? 0 }}</span></template>
            <template #cell-actions="{ row }"><div class="flex flex-wrap gap-2">
              <button class="btn btn-secondary btn-sm" :data-testid="`permissions-role-${row.id}`" @click="permissionRole = row">{{ rt('rbac.permissions') }}</button>
              <button v-if="auth.hasPermission('roles.update') && canManageRole(auth, row)" class="btn btn-secondary btn-sm" :data-testid="`edit-role-${row.id}`" @click="editingRole = row; editorOpen = true">{{ rt('rbac.edit') }}</button>
              <button v-if="auth.hasPermission('roles.delete') && canManageRole(auth, row)" class="btn btn-secondary btn-sm text-red-600" :data-testid="`delete-role-${row.id}`" @click="deletingRole = row; deleteError = ''">{{ rt('rbac.delete') }}</button>
            </div></template>
            <template #empty><p class="p-6 text-center text-gray-500">{{ rt('rbac.noResults') }}</p></template>
          </DataTable>
        </template>
        <div v-else-if="auth.hasPermission('permissions.read')" class="overflow-auto p-5">
          <p v-if="loadingCatalog" role="status">{{ rt('rbac.loading') }}</p>
          <div v-else-if="catalogError" role="alert" class="text-red-600">{{ catalogError }} <button class="btn btn-secondary" @click="loadCatalog">{{ rt('rbac.retry') }}</button></div>
          <PermissionPicker v-else :permissions="catalog" :model-value="[]" disabled />
        </div>
      </template>
    </TablePageLayout>
    <RoleEditorDialog v-if="editorOpen" :show="editorOpen" :role="editingRole" @close="editorOpen = false" @saved="loadRoles" />
    <RolePermissionsDialog v-if="permissionRole" :show="true" :role="permissionRole" @close="permissionRole = null" @saved="loadRoles" />
    <BaseDialog :show="!!deletingRole" :title="rt('rbac.delete')" :show-close-button="!deleting" :close-on-escape="!deleting" @close="cancelDelete">
      <p class="text-sm text-gray-600 dark:text-dark-400">{{ rt('rbac.deleteConfirm') }}</p><p class="mt-2 font-medium">{{ roleName(deletingRole) }}</p>
      <p v-if="deleteError" role="alert" class="mt-3 text-sm text-red-600">{{ deleteError }}</p>
      <template #footer><div class="flex justify-end gap-3"><button class="btn btn-secondary" :disabled="deleting" @click="cancelDelete">{{ rt('rbac.cancel') }}</button><button class="btn btn-danger" data-testid="confirm-delete-role" :disabled="deleting" @click="confirmDelete">{{ deleting ? rt('rbac.saving') : rt('rbac.delete') }}</button></div></template>
    </BaseDialog>
  </AppLayout>
</template>
<script setup lang="ts">
import { computed, ref, watch, onBeforeUnmount } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import TablePageLayout from '@/components/layout/TablePageLayout.vue'
import DataTable from '@/components/common/DataTable.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import PermissionPicker from '@/components/admin/rbac/PermissionPicker.vue'
import RoleEditorDialog from '@/components/admin/rbac/RoleEditorDialog.vue'
import RolePermissionsDialog from '@/components/admin/rbac/RolePermissionsDialog.vue'
import { rbacMessages } from '@/components/admin/rbac/messages'
import { useRBACLabels } from '@/components/admin/rbac/labels'
import { canManageRole, isProtectedRole, withinCeiling, rbacError } from '@/components/admin/rbac/policy'
import { rbacAPI, type RBACRole, type RBACPermission } from '@/api/admin/rbac'
import { useAuthStore } from '@/stores/auth'
import { useAppStore } from '@/stores/app'
const { t: rt } = useI18n({ useScope: 'local', messages: rbacMessages })
const { roleName, roleDescription } = useRBACLabels()
const auth = useAuthStore(), app = useAppStore()
const roles = ref<RBACRole[]>([]), catalog = ref<RBACPermission[]>([])
const loadingRoles = ref(false), loadingCatalog = ref(false), roleError = ref(''), catalogError = ref('')
const tab = ref(auth.hasPermission('roles.read') ? 'roles' : 'permissions'), search = ref('')
const editorOpen = ref(false), editingRole = ref<RBACRole | null>(null), permissionRole = ref<RBACRole | null>(null)
const deletingRole = ref<RBACRole | null>(null), deleting = ref(false), deleteError = ref('')
let rolesGeneration = 0, catalogGeneration = 0
const columns = computed(() => [
  { key: 'name', label: rt('rbac.name') }, { key: 'description', label: rt('rbac.description') },
  { key: 'permissions', label: rt('rbac.permissions') }, { key: 'actions', label: rt('rbac.actions') }
])
const filteredRoles = computed(() => roles.value.filter(role => `${role.name} ${role.code} ${role.description} ${roleName(role)} ${roleDescription(role)}`.toLowerCase().includes(search.value.trim().toLowerCase())))
async function loadRoles() {
  const seq = ++rolesGeneration
  roles.value = []; roleError.value = ''
  if (!auth.hasPermission('roles.read')) return
  loadingRoles.value = true
  try { const data = await rbacAPI.listRoles(); if (seq === rolesGeneration) roles.value = data ?? [] }
  catch (e) { if (seq === rolesGeneration) roleError.value = rbacError(e, rt('rbac.loadFailed')) }
  finally { if (seq === rolesGeneration) loadingRoles.value = false }
}
async function loadCatalog() {
  const seq = ++catalogGeneration
  catalog.value = []; catalogError.value = ''
  if (!auth.hasPermission('permissions.read')) return
  loadingCatalog.value = true
  try { const data = await rbacAPI.listPermissions(); if (seq === catalogGeneration) catalog.value = data ?? [] }
  catch (e) { if (seq === catalogGeneration) catalogError.value = rbacError(e, rt('rbac.loadFailed')) }
  finally { if (seq === catalogGeneration) loadingCatalog.value = false }
}
watch(() => auth.hasPermission('roles.read'), () => { if (!auth.hasPermission('roles.read')) tab.value = 'permissions'; void loadRoles() }, { immediate: true })
watch(() => auth.hasPermission('permissions.read'), loadCatalog, { immediate: true })
onBeforeUnmount(() => { rolesGeneration++; catalogGeneration++ })
function cancelDelete() { if (!deleting.value) deletingRole.value = null }
async function confirmDelete() {
  const role = deletingRole.value
  if (!role || deleting.value || !auth.hasPermission('roles.delete') || !canManageRole(auth, role)) return
  deleting.value = true; deleteError.value = ''
  try {
    await rbacAPI.deleteRole(role.id)
    try { await auth.refreshUser() } catch { app.showError(rt('rbac.refreshFailed')) }
    app.showSuccess(rt('rbac.deleted')); deletingRole.value = null; await loadRoles()
  } catch (e) { deleteError.value = rbacError(e, rt('rbac.deleteFailed')) }
  finally { deleting.value = false }
}
</script>
