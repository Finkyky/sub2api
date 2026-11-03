<template>
  <div class="space-y-4">
    <input v-model="query" type="search" class="input" :placeholder="rt('rbac.searchPermissions')" :aria-label="rt('rbac.searchPermissions')" />
    <p class="text-sm text-gray-500 dark:text-dark-400">{{ disabled ? rt('rbac.readOnly') : rt('rbac.ceilingHint') }}</p>
    <p v-if="!groups.length" class="py-4 text-sm text-gray-500">{{ rt('rbac.noResults') }}</p>
    <fieldset v-for="group in groups" :key="group.resource" class="rounded-xl border border-gray-200 p-4 dark:border-dark-600">
      <legend class="px-2 text-sm font-semibold text-gray-700 dark:text-gray-200">{{ resourceName(group.resource) }}</legend>
      <div class="grid gap-3 sm:grid-cols-2">
        <label v-for="permission in group.items" :key="permission.code" class="flex items-start gap-3 text-sm" :class="{ 'opacity-60': locked(permission.code) }">
          <input type="checkbox" class="mt-1 rounded border-gray-300 text-primary-600 focus:ring-primary-500" :value="permission.code" :checked="modelValue.includes(permission.code)" :disabled="locked(permission.code)" @change="toggle(permission.code, ($event.target as HTMLInputElement).checked)" />
          <span class="min-w-0"><span class="block break-all font-medium text-gray-800 dark:text-gray-200">{{ permissionName(permission) }}</span><span class="block break-all font-mono text-xs text-gray-500 dark:text-dark-400">{{ permission.code }}</span><span v-if="permissionDescription(permission)" class="block text-gray-500 dark:text-dark-400">{{ permissionDescription(permission) }}</span></span>
        </label>
      </div>
    </fieldset>
  </div>
</template>
<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAuthStore } from '@/stores/auth'
import type { RBACPermission } from '@/api/admin/rbac'
import { rbacMessages } from './messages'
import { useRBACLabels } from './labels'
const props = withDefaults(defineProps<{ permissions: RBACPermission[]; modelValue: string[]; disabled?: boolean }>(), { disabled: false })
const emit = defineEmits<{ 'update:modelValue': [codes: string[]] }>()
const { t: rt } = useI18n({ useScope: 'local', messages: rbacMessages })
const auth = useAuthStore()
const { resourceName, permissionName, permissionDescription } = useRBACLabels()
const query = ref('')
const groups = computed(() => {
  const q = query.value.trim().toLowerCase()
  const grouped = new Map<string, RBACPermission[]>()
  for (const p of props.permissions) {
    if (q && !`${p.resource} ${p.code} ${p.name} ${p.description} ${resourceName(p.resource)} ${permissionName(p)} ${permissionDescription(p)}`.toLowerCase().includes(q)) continue
    grouped.set(p.resource, [...(grouped.get(p.resource) || []), p])
  }
  return Array.from(grouped, ([resource, items]) => ({ resource, items })).sort((a, b) => a.resource.localeCompare(b.resource))
})
const locked = (code: string) => props.disabled || !auth.hasPermission(code)
function toggle(code: string, checked: boolean) {
  if (locked(code)) return
  const selected = new Set(props.modelValue)
  if (checked) selected.add(code)
  else selected.delete(code)
  emit('update:modelValue', [...selected])
}
</script>
