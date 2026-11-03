<template>
  <section data-testid="status-panel" class="rounded-lg border border-gray-200 bg-gray-50 p-4 dark:border-dark-700 dark:bg-dark-800">
    <h3 class="text-sm font-semibold">{{ t('admin.pluginInstances.statusTitle') }}</h3>
    <p class="mt-1 text-xs text-gray-500">{{ t('admin.pluginInstances.localStatusHint') }}</p>
    <p v-if="loading" class="mt-3 text-sm" role="status">{{ t('admin.pluginInstances.loading') }}</p>
    <p v-else-if="error" class="mt-3 text-sm text-red-600" role="alert">{{ error }}</p>
    <template v-else-if="status">
      <dl class="mt-3 grid grid-cols-2 gap-3 text-xs md:grid-cols-4">
        <div><dt class="text-gray-500">{{ t('admin.pluginInstances.state') }}</dt><dd class="mt-1 font-medium">{{ status.enabled ? t('admin.pluginInstances.enabled') : t('admin.pluginInstances.disabled') }}</dd></div>
        <div><dt class="text-gray-500">{{ t('admin.pluginInstances.currentRevision') }}</dt><dd class="mt-1 font-mono">{{ status.current_revision }}</dd></div>
        <div><dt class="text-gray-500">{{ t('admin.pluginInstances.runtimePresent') }}</dt><dd class="mt-1">{{ yesNo(status.runtime_present) }}</dd></div>
        <div><dt class="text-gray-500">{{ t('admin.pluginInstances.leaseRefs') }}</dt><dd class="mt-1 font-mono">{{ status.lease_refs }}</dd></div>
        <div><dt class="text-gray-500">{{ t('admin.pluginInstances.retired') }}</dt><dd class="mt-1">{{ yesNo(status.retired) }}</dd></div>
        <div><dt class="text-gray-500">{{ t('admin.pluginInstances.closed') }}</dt><dd class="mt-1">{{ yesNo(status.closed) }}</dd></div>
        <div class="col-span-2"><dt class="text-gray-500">{{ t('admin.pluginInstances.lastError') }}</dt><dd class="mt-1">{{ lastError(status.last_error) }}</dd></div>
      </dl>
      <div v-if="status.runtimes.length" class="mt-4 overflow-x-auto">
        <table class="w-full text-left text-xs">
          <thead><tr><th class="py-2">{{ t('admin.pluginInstances.revision') }}</th><th>{{ t('admin.pluginInstances.leaseRefs') }}</th><th>{{ t('admin.pluginInstances.localCurrent') }}</th><th>{{ t('admin.pluginInstances.retired') }}</th><th>{{ t('admin.pluginInstances.closed') }}</th></tr></thead>
          <tbody><tr v-for="runtime in status.runtimes" :key="runtime.revision" class="border-t border-gray-200 dark:border-dark-700"><td class="py-2 font-mono">{{ runtime.revision }}</td><td>{{ runtime.lease_refs }}</td><td>{{ yesNo(runtime.local_current) }}</td><td>{{ yesNo(runtime.retired) }}</td><td>{{ yesNo(runtime.closed) }}</td></tr></tbody>
        </table>
      </div>
    </template>
  </section>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import type { PluginInstanceStatus } from '@/api/admin/pluginInstances'
defineProps<{ status: PluginInstanceStatus | null; loading?: boolean; error?: string }>()
const { t } = useI18n()
function lastError(code: string) {
  if (!code) return t('admin.pluginInstances.noError')
  if (code === 'runtime_start_failed') return t('admin.pluginInstances.runtimeStartFailed')
  if (code === 'runtime_call_failed') return t('admin.pluginInstances.runtimeCallFailed')
  if (code === 'runtime_unavailable') return t('admin.pluginInstances.runtimeUnavailable')
  return t('admin.pluginInstances.runtimeError')
}
function yesNo(value: boolean) { return value ? t('admin.pluginInstances.yes') : t('admin.pluginInstances.no') }
</script>
