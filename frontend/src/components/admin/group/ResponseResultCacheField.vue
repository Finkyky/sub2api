<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import type { ResponseResultCacheInput } from '@/types'
const props = defineProps<{ modelValue: ResponseResultCacheInput; disabled?: boolean }>()
const emit = defineEmits<{ 'update:modelValue': [value: ResponseResultCacheInput] }>()
const { t } = useI18n()
const update = (patch: Partial<ResponseResultCacheInput>) => {
 if (!props.disabled) emit('update:modelValue', {
  enabled: patch.enabled ?? props.modelValue.enabled,
  ttl_seconds: patch.ttl_seconds ?? props.modelValue.ttl_seconds,
  max_response_bytes: patch.max_response_bytes ?? props.modelValue.max_response_bytes,
  allow_group_sharing: patch.allow_group_sharing ?? props.modelValue.allow_group_sharing,
 })
}
const checked = (e: Event) => (e.target as HTMLInputElement).checked
const number = (e: Event) => Number((e.target as HTMLInputElement).value)
</script>
<template>
 <fieldset :disabled="disabled" class="space-y-3 rounded-lg border border-gray-200 p-4 dark:border-dark-600">
  <label class="flex items-center gap-2 text-sm font-medium text-gray-800 dark:text-gray-100">
   <input data-testid="cache-enabled" type="checkbox" :checked="modelValue.enabled" :disabled="disabled" @change="update({ enabled: checked($event) })" />
   {{ t('admin.groups.resultCache.title') }}
  </label>
  <p class="input-hint">{{ t('admin.groups.resultCache.hint') }}</p>
  <div v-if="modelValue.enabled" class="space-y-3">
   <div class="grid grid-cols-1 gap-3 sm:grid-cols-2">
    <label class="block"><span class="input-label">{{ t('admin.groups.resultCache.ttl') }}</span>
     <input data-testid="cache-ttl" class="input" type="number" required min="1" max="86400" step="1" :value="modelValue.ttl_seconds" :disabled="disabled" @input="update({ ttl_seconds: number($event) })" />
    </label>
    <label class="block"><span class="input-label">{{ t('admin.groups.resultCache.maxBytes') }}</span>
     <input data-testid="cache-size" class="input" type="number" required min="1024" max="2097152" step="1" :value="modelValue.max_response_bytes" :disabled="disabled" @input="update({ max_response_bytes: number($event) })" />
    </label>
   </div>
   <label class="flex items-center gap-2 text-sm text-gray-700 dark:text-gray-200">
    <input data-testid="cache-sharing" type="checkbox" :checked="modelValue.allow_group_sharing" :disabled="disabled" @change="update({ allow_group_sharing: checked($event) })" />
    {{ t('admin.groups.resultCache.sharing') }}
   </label>
   <p class="input-hint">{{ t('admin.groups.resultCache.sharingHint') }}</p>
  </div>
 </fieldset>
</template>
