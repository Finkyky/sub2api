<template>
  <div class="space-y-2">
    <p class="rounded-lg border border-amber-200 bg-amber-50 px-3 py-2 text-xs text-amber-800 dark:border-amber-900/50 dark:bg-amber-950/20 dark:text-amber-200">{{ t('admin.pluginInstances.configWriteOnly') }}</p>
    <label class="block text-sm font-medium">
      {{ t('admin.pluginInstances.configJson') }}
      <textarea data-testid="config-json" :value="modelValue" :disabled="disabled" rows="7" spellcheck="false" autocomplete="off" class="input mt-1 w-full font-mono text-xs" @input="emit('update:modelValue', ($event.target as HTMLTextAreaElement).value)" />
    </label>
  </div>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
defineProps<{ modelValue: string; disabled?: boolean }>()
const emit = defineEmits<{ 'update:modelValue': [value: string] }>()
const { t } = useI18n()
</script>
