<script setup lang="ts">
// A self-contained "label + value + proportional bar" card. Unlike the old
// fixed-width-label rows, the label wraps (break-all) so long domains / IPs are
// shown in full, and the card is sized by its parent grid — drop several into a
// `grid grid-cols-1 sm:grid-cols-2 xl:grid-cols-3` and they reflow to the
// screen width.
defineProps<{
  label: string
  value: string
  pct: number
  color?: string
  dotColor?: string
  badge?: string
  sub?: string
  title?: string
  mono?: boolean
}>()
</script>

<template>
  <div
    class="rounded-lg border border-slate-100 dark:border-slate-700/60 bg-slate-50/60 dark:bg-slate-900/30 p-3"
    :title="title"
  >
    <div class="flex items-start gap-2">
      <span
        v-if="dotColor"
        class="mt-1 inline-block w-2.5 h-2.5 rounded-sm shrink-0"
        :style="{ background: dotColor }"
      ></span>
      <div class="min-w-0 flex-1 leading-snug">
        <span :class="['break-all text-xs text-slate-700 dark:text-slate-200', mono ? 'font-mono' : '']">{{ label }}</span>
        <span
          v-if="badge"
          class="ml-1 align-middle px-1 rounded text-[9px] leading-4 bg-sky-100 text-sky-600 dark:bg-sky-900/40 dark:text-sky-300"
        >{{ badge }}</span>
        <span v-if="sub" class="text-slate-400 break-all"> · {{ sub }}</span>
      </div>
      <span class="shrink-0 text-xs tabular-nums text-slate-500 dark:text-slate-400">{{ value }}</span>
    </div>
    <div class="mt-2 bg-slate-100 dark:bg-slate-700/50 rounded h-1.5 overflow-hidden">
      <div class="h-full rounded" :style="{ width: Math.max(1.5, pct) + '%', background: color || '#22c55e' }"></div>
    </div>
  </div>
</template>
