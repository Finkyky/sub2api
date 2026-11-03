<template>
  <AppLayout><div class="space-y-6"><PluginSectionsNav /><PluginGroupBindingsPanel :group-id="groupId" /></div></AppLayout>
</template>
<script setup lang="ts">
import { computed } from 'vue'
import { useRoute } from 'vue-router'
import AppLayout from '@/components/layout/AppLayout.vue'
import PluginSectionsNav from '@/components/admin/plugin/PluginSectionsNav.vue'
import PluginGroupBindingsPanel from '@/components/admin/plugin/PluginGroupBindingsPanel.vue'
const route = useRoute()
function positiveId(value: unknown) { const id = Number(value); return Number.isSafeInteger(id) && id > 0 ? id : undefined }
const groupId = computed(() => positiveId(route.query.group_id))
</script>
