<template>
  <AppLayout><div class="space-y-6"><PluginSectionsNav /><PluginInstancesPanel /></div></AppLayout>
</template>
<script setup lang="ts">
import AppLayout from '@/components/layout/AppLayout.vue'
import PluginSectionsNav from '@/components/admin/plugin/PluginSectionsNav.vue'
import PluginInstancesPanel from '@/components/admin/plugin/PluginInstancesPanel.vue'
</script>
