<script setup lang="ts">
defineProps<{ rows?: number; cols?: number }>()
</script>

<template>
  <div class="animate-pulse space-y-3">
    <div v-for="r in (rows || 3)" :key="r" class="flex gap-4">
      <div v-for="c in (cols || 4)" :key="c" class="h-4 bg-slate-200 dark:bg-slate-700 rounded flex-1" />
    </div>
  </div>
</template>
