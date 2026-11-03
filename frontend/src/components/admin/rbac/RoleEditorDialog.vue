<template>
  <BaseDialog :show="show" :title="role ? rt('rbac.edit') : rt('rbac.create')" :show-close-button="!saving" :close-on-escape="!saving" @close="close">
    <form id="rbac-role-form" class="space-y-4" @submit.prevent="save">
      <p v-if="locked" class="text-sm text-amber-700 dark:text-amber-400">{{ role && isProtectedRole(role) ? rt('rbac.lockedRole') : rt('rbac.readOnly') }}</p>
      <label class="block"><span class="input-label">{{ rt('rbac.code') }}</span><input v-model="code" data-testid="role-code" class="input" required pattern="[a-z][a-z0-9_-]{0,99}" maxlength="100" :disabled="!!role || locked || saving" /></label>
      <label class="block"><span class="input-label">{{ rt('rbac.name') }}</span><input v-model="name" data-testid="role-name" class="input" required :disabled="locked || saving" /></label>
      <label class="block"><span class="input-label">{{ rt('rbac.description') }}</span><textarea v-model="description" class="input" rows="3" maxlength="10000" :disabled="locked || saving" /></label>
      <p v-if="error" role="alert" class="text-sm text-red-600 dark:text-red-400">{{ error }}</p>
    </form>
    <template #footer><div class="flex justify-end gap-3">
      <button type="button" class="btn btn-secondary" data-testid="cancel" :disabled="saving" @click="close">{{ rt('rbac.cancel') }}</button>
      <button v-if="!locked" type="submit" form="rbac-role-form" class="btn btn-primary" :disabled="saving">{{ saving ? rt('rbac.saving') : rt('rbac.save') }}</button>
    </div></template>
  </BaseDialog>
</template>
<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import { useAuthStore } from '@/stores/auth'
import { useAppStore } from '@/stores/app'
import { rbacAPI, type RBACRole } from '@/api/admin/rbac'
import { canManageRole, isProtectedRole, rbacError } from './policy'
import { rbacMessages } from './messages'
const props = defineProps<{ show: boolean; role: RBACRole | null }>()
const emit = defineEmits<{ close: []; saved: [] }>()
const { t: rt } = useI18n({ useScope: 'local', messages: rbacMessages })
const auth = useAuthStore(), app = useAppStore()
const code = ref(''), name = ref(''), description = ref(''), error = ref(''), saving = ref(false)
const locked = computed(() => props.role
  ? !auth.hasPermission('roles.update') || !canManageRole(auth, props.role)
  : !auth.hasPermission('roles.create'))
watch(() => [props.show, props.role] as const, () => {
  code.value = props.role?.code || ''; name.value = props.role?.name || ''; description.value = props.role?.description || ''; error.value = ''
}, { immediate: true })
function close() { if (!saving.value) emit('close') }
async function save() {
  if (saving.value || locked.value) return
  if (!name.value.trim() || [...name.value.trim()].length > 150 || !/^[a-z][a-z0-9_-]{0,99}$/.test(code.value.trim()) || code.value.trim() === 'super_admin') {
    error.value = rt('rbac.invalid'); return
  }
  saving.value = true; error.value = ''
  try {
    const metadata = { name: name.value.trim(), description: description.value.trim() }
    if (props.role) await rbacAPI.updateRole(props.role.id, metadata)
    else await rbacAPI.createRole({ code: code.value.trim(), ...metadata })
    app.showSuccess(rt('rbac.saved')); emit('saved'); emit('close')
  } catch (e) { error.value = rbacError(e, rt('rbac.saveFailed')) }
  finally { saving.value = false }
}
</script>
