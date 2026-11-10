<script setup lang="ts">
import { computed, defineAsyncComponent } from 'vue'
import { useRoute } from 'vue-router'
import OverviewView from '@/views/OverviewView.vue'

const TerminalView = defineAsyncComponent(() => import('@/views/TerminalView.vue'))
const WorkbenchView = defineAsyncComponent(() => import('@/views/WorkbenchView.vue'))
const LogView = defineAsyncComponent(() => import('@/views/LogView.vue'))
const route = useRoute()
const view = computed(() => {
  if (route.query.view === 'logs') return LogView
  if (route.query.view === 'terminal' && __TERMINAL_ENABLED__) return TerminalView
  return route.query.view === 'overview' ? OverviewView : WorkbenchView
})
</script>

<template>
  <KeepAlive include="TerminalView">
    <component :is="view" />
  </KeepAlive>
</template>
