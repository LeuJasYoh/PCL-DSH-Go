<script setup lang="ts">
import { computed } from 'vue'
import { useRoute } from 'vue-router'

import AppShell from '@/components/AppShell.vue'
import AppToast from '@/components/AppToast.vue'

const route = useRoute()
const showShell = computed(() => route.meta.layout !== 'blank')
</script>

<template>
  <AppShell v-if="showShell">
    <router-view />
  </AppShell>
  <router-view v-else />
  <AppToast />
</template>
