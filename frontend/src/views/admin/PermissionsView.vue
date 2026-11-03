<template>
  <AppLayout>
    <TablePageLayout>
      <template #actions>
        <div>
          <h1 class="text-2xl font-bold text-gray-900 dark:text-white">{{ rt('rbac.catalog') }}</h1>
          <p class="mt-1 text-sm text-gray-500 dark:text-dark-400">{{ rt('rbac.catalogDescription') }}</p>
        </div>
      </template>
      <template #table>
        <div class="overflow-auto p-5">
          <p v-if="!auth.hasPermission('permissions.read')" role="status">{{ rt('rbac.noPermission') }}</p>
          <p v-else-if="loading" role="status">{{ rt('rbac.loading') }}</p>
          <div v-else-if="error" role="alert" class="space-y-3 text-sm text-red-600 dark:text-red-400">
            <p>{{ error }}</p>
            <button class="btn btn-secondary" data-testid="retry-catalog" @click="load">{{ rt('rbac.retry') }}</button>
          </div>
          <PermissionPicker v-else :permissions="permissions" :model-value="[]" disabled />
        </div>
      </template>
    </TablePageLayout>
  </AppLayout>
</template>
<script setup lang="ts">
import { onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import TablePageLayout from '@/components/layout/TablePageLayout.vue'
import PermissionPicker from '@/components/admin/rbac/PermissionPicker.vue'
import { rbacMessages } from '@/components/admin/rbac/messages'
import { rbacError } from '@/components/admin/rbac/policy'
import { rbacAPI, type RBACPermission } from '@/api/admin/rbac'
import { useAuthStore } from '@/stores/auth'
const { t: rt } = useI18n({ useScope: 'local', messages: rbacMessages })
const auth = useAuthStore()
const permissions = ref<RBACPermission[]>([])
const loading = ref(false), error = ref('')
let generation = 0
async function load() {
  const seq = ++generation
  permissions.value = []; error.value = ''; loading.value = false
  if (!auth.hasPermission('permissions.read')) return
  loading.value = true
  try {
    const data = await rbacAPI.listPermissions()
    if (seq === generation) permissions.value = data ?? []
  } catch (e) {
    if (seq === generation) error.value = rbacError(e, rt('rbac.loadFailed'))
  } finally {
    if (seq === generation) loading.value = false
  }
}
watch(() => auth.hasPermission('permissions.read'), load, { immediate: true })
onBeforeUnmount(() => { generation++ })
</script>
