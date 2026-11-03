<template>
  <AppLayout><div class="space-y-6"><PluginSectionsNav /><PluginTasksPanel :instance-id="instanceId" /></div></AppLayout>
</template>
<script setup lang="ts">
import { computed } from 'vue'
import { useRoute } from 'vue-router'
import AppLayout from '@/components/layout/AppLayout.vue'
import PluginSectionsNav from '@/components/admin/plugin/PluginSectionsNav.vue'
import PluginTasksPanel from '@/components/admin/plugin/PluginTasksPanel.vue'
const route = useRoute()
function positiveId(value: unknown) { const id = Number(value); return Number.isSafeInteger(id) && id > 0 ? id : undefined }
const instanceId = computed(() => positiveId(route.query.instance_id))
</script>
