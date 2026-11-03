<template>
  <section class="space-y-3">
    <div class="flex items-center justify-between gap-3">
      <h3 class="text-sm font-semibold">{{ t('admin.pluginInstances.bindings') }}</h3>
      <button v-if="!readonly" data-testid="add-binding" type="button" class="btn btn-secondary btn-sm" :disabled="disabled || hooks.length === 0 || modelValue.length >= 128" @click="add">{{ t('admin.pluginInstances.addBinding') }}</button>
    </div>
    <p v-if="hooks.length === 0" class="text-sm text-gray-500">{{ t('admin.pluginInstances.noHooks') }}</p>
    <p v-else-if="modelValue.length === 0" class="text-sm text-gray-500">{{ t('admin.pluginInstances.noBindings') }}</p>
    <fieldset v-for="(binding, index) in modelValue" :key="index" :disabled="readonly || disabled" class="rounded-lg border border-gray-200 p-3 dark:border-dark-700">
      <legend class="px-1 text-xs text-gray-500">{{ t('admin.pluginInstances.bindingNumber', { number: index + 1 }) }}</legend>
      <div class="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
        <label class="text-xs font-medium">{{ t('admin.pluginInstances.group') }}
          <select v-if="groups.length" data-testid="binding-group" :disabled="readonly || disabled" :value="binding.group_id" class="input mt-1 w-full" @change="update(index, { group_id: Number(($event.target as HTMLSelectElement).value) })">
            <option v-if="!groups.some(group => group.id === binding.group_id)" :value="binding.group_id">#{{ binding.group_id }}</option>
            <option v-for="group in groups" :key="group.id" :value="group.id">{{ group.name }} (#{{ group.id }})</option>
          </select>
          <input v-else data-testid="binding-group" type="number" min="1" step="1" :disabled="readonly || disabled" :value="binding.group_id" class="input mt-1 w-full" @input="update(index, { group_id: Number(($event.target as HTMLInputElement).value) })" />
        </label>
        <label class="text-xs font-medium">{{ t('admin.pluginInstances.entry') }}
          <select data-testid="binding-entry" :value="binding.entry" class="input mt-1 w-full" @change="changeEntry(index, ($event.target as HTMLSelectElement).value)">
            <option v-if="!hookFor(binding)" :value="binding.entry">{{ binding.entry }}</option>
            <option v-for="hook in hooks" :key="hook.name" :value="hook.name">{{ hook.name }}</option>
          </select>
        </label>
        <label class="text-xs font-medium">{{ t('admin.pluginInstances.stage') }}
          <select data-testid="binding-stage" :value="binding.stage" class="input mt-1 w-full" @change="update(index, { stage: ($event.target as HTMLSelectElement).value as PluginGroupBinding['stage'] })">
            <option v-for="stage in hookFor(binding)?.stages || []" :key="stage" :value="stage">{{ stage }}</option>
          </select>
        </label>
        <label class="text-xs font-medium">{{ t('admin.pluginInstances.protocol') }}
          <select data-testid="binding-protocol" :value="binding.protocol" class="input mt-1 w-full" @change="update(index, { protocol: ($event.target as HTMLSelectElement).value })">
            <option v-for="protocol in hookFor(binding)?.protocols || []" :key="protocol" :value="protocol">{{ protocol }}</option>
          </select>
        </label>
        <label class="text-xs font-medium">{{ t('admin.pluginInstances.sortOrder') }}
          <input data-testid="binding-order" type="number" min="-100000" max="100000" step="1" :value="binding.sort_order" class="input mt-1 w-full" @input="update(index, { sort_order: Number(($event.target as HTMLInputElement).value) })" />
        </label>
        <div class="flex flex-wrap items-center gap-4 text-xs">
          <label class="flex items-center gap-2"><input data-testid="binding-enabled" type="checkbox" :checked="binding.enabled" @change="update(index, { enabled: ($event.target as HTMLInputElement).checked })" />{{ t('admin.pluginInstances.enabled') }}</label>
          <label class="flex items-center gap-2"><input data-testid="binding-replace" type="checkbox" :checked="binding.can_replace" :disabled="readonly || disabled || !hookFor(binding)?.can_replace" @change="update(index, { can_replace: ($event.target as HTMLInputElement).checked })" />{{ t('admin.pluginInstances.canReplace') }}</label>
        </div>
      </div>
      <button v-if="!readonly" data-testid="remove-binding" type="button" class="mt-3 text-xs text-red-600" @click="remove(index)">{{ t('admin.pluginInstances.removeBinding') }}</button>
    </fieldset>
    <p v-if="!readonly && !groups.length" class="text-xs text-gray-500">{{ t('admin.pluginInstances.groupIdHint') }}</p>
  </section>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import type { PluginGroupBinding, PluginHookDeclaration } from '@/api/admin/pluginInstances'
const props = defineProps<{ modelValue: PluginGroupBinding[]; instanceId: number; hooks: PluginHookDeclaration[]; groups: { id: number; name: string }[]; readonly?: boolean; disabled?: boolean }>()
const emit = defineEmits<{ 'update:modelValue': [value: PluginGroupBinding[]] }>()
const { t } = useI18n()
function hookFor(binding: PluginGroupBinding) { return props.hooks.find(hook => hook.name === binding.entry) }
function update(index: number, patch: Partial<PluginGroupBinding>) {
  if (props.readonly || props.disabled) return
  emit('update:modelValue', props.modelValue.map((binding, i) => i === index ? { ...binding, ...patch } : binding))
}
function changeEntry(index: number, name: string) {
  const hook = props.hooks.find(item => item.name === name)
  if (!hook) return
  const binding = props.modelValue[index]
  update(index, { entry: name, stage: hook.stages.includes(binding.stage) ? binding.stage : hook.stages[0], protocol: hook.protocols.includes(binding.protocol) ? binding.protocol : hook.protocols[0], can_replace: !!hook.can_replace && binding.can_replace })
}
function add() {
  const hook = props.hooks[0]
  if (!hook || props.readonly || props.disabled || props.modelValue.length >= 128) return
  emit('update:modelValue', [...props.modelValue, { instance_id: props.instanceId, group_id: props.groups[0]?.id || 0, stage: hook.stages[0], entry: hook.name, protocol: hook.protocols[0], sort_order: props.modelValue.length, enabled: true, can_replace: false }])
}
function remove(index: number) {
  if (props.readonly || props.disabled) return
  emit('update:modelValue', props.modelValue.filter((_, i) => i !== index))
}
</script>
