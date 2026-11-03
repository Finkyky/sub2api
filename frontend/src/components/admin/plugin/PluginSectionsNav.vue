<template>
  <nav :aria-label="t('admin.pluginSections.label')" class="flex flex-wrap gap-2 border-b border-gray-200 pb-3 dark:border-dark-700">
    <RouterLink v-for="section in visible" :key="section.path" :to="section.path" class="rounded-lg px-3 py-2 text-sm text-gray-600 hover:bg-gray-100 dark:text-gray-300 dark:hover:bg-dark-700" active-class="bg-primary-50 font-semibold text-primary-700 dark:bg-primary-900/20 dark:text-primary-400">{{ t(section.label) }}</RouterLink>
  </nav>
</template>
<script setup lang="ts">
import { computed } from 'vue'
import { RouterLink } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useAuthStore } from '@/stores/auth'
const { t } = useI18n(), auth = useAuthStore()
const sections = [
  { path: '/admin/plugins', label: 'admin.pluginSections.library', permission: 'plugins.read' },
  { path: '/admin/plugin-instances', label: 'admin.pluginSections.instances', permission: 'plugin_instances.read' },
  { path: '/admin/plugin-bindings', label: 'admin.pluginSections.bindings', permission: 'plugin_instances.read' },
  { path: '/admin/plugin-tasks', label: 'admin.pluginSections.tasks', permission: 'plugin_tasks.read' },
  { path: '/admin/plugin-runs', label: 'admin.pluginSections.runs', permission: 'plugin_runs.read' }
]
const visible = computed(() => sections.filter(section => auth.hasPermission(section.permission)))
</script>
