import { ref } from 'vue'

const isDark = ref(false)

function apply() {
  document.documentElement.classList.toggle('dark', isDark.value)
}

function init() {
  const saved = localStorage.getItem('zenith-dark-mode')
  if (saved !== null) {
    isDark.value = saved === 'true'
  } else {
    isDark.value = window.matchMedia('(prefers-color-scheme: dark)').matches
  }
  apply()
}

function toggle() {
  isDark.value = !isDark.value
  localStorage.setItem('zenith-dark-mode', String(isDark.value))
  apply()
}

export function useDarkMode() {
  return { isDark, toggle, init }
}
