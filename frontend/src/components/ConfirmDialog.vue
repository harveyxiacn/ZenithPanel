<script setup lang="ts">
import { useConfirm } from '../composables/useConfirm'
import { ExclamationTriangleIcon } from '@heroicons/vue/24/outline'

const { visible, title, message, confirmText, cancelText, variant, onConfirm, onCancel } = useConfirm()

const btnClass: Record<string, string> = {
  danger: 'bg-rose-600 hover:bg-rose-500 text-white',
  warning: 'bg-amber-600 hover:bg-amber-500 text-white',
  default: 'btn-primary',
}
</script>

<template>
  <Teleport to="body">
    <Transition
      enter-active-class="transition duration-200"
      enter-from-class="opacity-0"
      leave-active-class="transition duration-150"
      leave-to-class="opacity-0"
    >
      <div v-if="visible" class="fixed inset-0 z-[90] flex items-center justify-center bg-black/50 backdrop-blur-sm p-4">
        <div class="bg-white rounded-2xl shadow-2xl max-w-md w-full p-6">
          <div class="flex items-start gap-4">
            <div class="w-10 h-10 rounded-full bg-rose-100 flex items-center justify-center shrink-0">
              <ExclamationTriangleIcon class="w-5 h-5 text-rose-600" />
            </div>
            <div>
              <h3 class="text-lg font-semibold text-slate-900">{{ title }}</h3>
              <p class="mt-1 text-sm text-slate-500">{{ message }}</p>
            </div>
          </div>
          <div class="flex justify-end gap-3 mt-6">
            <button @click="onCancel" class="btn-secondary text-sm">{{ cancelText }}</button>
            <button @click="onConfirm" :class="['px-4 py-2.5 rounded-xl text-sm font-medium transition-all active:scale-95', btnClass[variant]]">
              {{ confirmText }}
            </button>
          </div>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>
