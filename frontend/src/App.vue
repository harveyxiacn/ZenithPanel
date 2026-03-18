<script setup lang="ts">
import { onMounted } from 'vue'
import { RouterView } from 'vue-router'
import ToastContainer from './components/ToastContainer.vue'
import ConfirmDialog from './components/ConfirmDialog.vue'
import { useDarkMode } from './composables/useDarkMode'

const { init: initDarkMode } = useDarkMode()
onMounted(() => initDarkMode())
</script>

<template>
  <div class="min-h-screen bg-slate-50 dark:bg-slate-900 text-slate-900 dark:text-slate-100 selection:bg-primary-200">
    <RouterView />
    <ToastContainer />
    <ConfirmDialog />
  </div>
</template>

<style>
/* Transitions for RouterView can be added here */
</style>
