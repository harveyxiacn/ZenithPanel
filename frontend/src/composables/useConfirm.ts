import { ref } from 'vue'

const visible = ref(false)
const title = ref('')
const message = ref('')
const confirmText = ref('Confirm')
const cancelText = ref('Cancel')
const variant = ref<'danger' | 'warning' | 'default'>('default')
let resolvePromise: ((value: boolean) => void) | null = null

export function useConfirm() {
  function confirm(opts: {
    title: string
    message: string
    confirmText?: string
    cancelText?: string
    variant?: 'danger' | 'warning' | 'default'
  }): Promise<boolean> {
    resolvePromise?.(false)
    resolvePromise = null
    title.value = opts.title
    message.value = opts.message
    confirmText.value = opts.confirmText || 'Confirm'
    cancelText.value = opts.cancelText || 'Cancel'
    variant.value = opts.variant || 'default'
    visible.value = true
    return new Promise((resolve) => { resolvePromise = resolve })
  }

  function onConfirm() {
    visible.value = false
    resolvePromise?.(true)
    resolvePromise = null
  }
  function onCancel() {
    visible.value = false
    resolvePromise?.(false)
    resolvePromise = null
  }

  return { visible, title, message, confirmText, cancelText, variant, confirm, onConfirm, onCancel }
}
