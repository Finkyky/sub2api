<template>
  <AppLayout>
    <div class="mx-auto max-w-lg p-8 text-center">
      <h1 class="text-xl font-semibold">{{ t('common.accessDenied') }}</h1>
      <p class="mt-3 text-gray-500">{{ t('common.accessDeniedHint') }}</p>
      <button class="btn btn-secondary mt-6" @click="logout">{{ t('nav.logout') }}</button>
    </div>
  </AppLayout>
</template>
<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import AppLayout from '@/components/layout/AppLayout.vue'
import { useAuthStore } from '@/stores/auth'
const { t } = useI18n()
const auth = useAuthStore()
const router = useRouter()
async function logout() { await auth.logout(); await router.replace('/login') }
</script>
