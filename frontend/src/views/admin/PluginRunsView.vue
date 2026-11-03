<template>
  <AppLayout><div class="space-y-6"><PluginSectionsNav /><PluginRunsPanel :task-id="taskId" :initial-run-id="runId" /></div></AppLayout>
</template>
<script setup lang="ts">
import { computed } from 'vue'
import { useRoute } from 'vue-router'
import AppLayout from '@/components/layout/AppLayout.vue'
import PluginSectionsNav from '@/components/admin/plugin/PluginSectionsNav.vue'
import PluginRunsPanel from '@/components/admin/plugin/PluginRunsPanel.vue'
const route = useRoute()
function positiveId(value: unknown) { const id = Number(value); return Number.isSafeInteger(id) && id > 0 ? id : undefined }
const taskId = computed(() => positiveId(route.query.task_id))
const runId = computed(() => positiveId(route.query.run_id))
</script>
