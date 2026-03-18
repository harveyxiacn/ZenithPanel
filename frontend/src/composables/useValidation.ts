import { ref, computed } from 'vue'

type Rule = (value: any) => string | true

export function useField(initialValue: any, rules: Rule[]) {
  const value = ref(initialValue)
  const touched = ref(false)
  const error = computed(() => {
    if (!touched.value) return ''
    for (const rule of rules) {
      const result = rule(value.value)
      if (result !== true) return result
    }
    return ''
  })
  const valid = computed(() => !error.value && touched.value)
  function blur() { touched.value = true }
  function reset() { value.value = initialValue; touched.value = false }
  return { value, error, valid, touched, blur, reset }
}

// Common validation rules
export const required = (msg = 'Required') =>
  (v: any) => (v !== '' && v != null && v !== undefined) || msg

export const minLength = (n: number, msg?: string) =>
  (v: string) => (v && v.length >= n) || (msg || `Minimum ${n} characters`)

export const portRange = (msg = 'Port must be 1-65535') =>
  (v: number) => (v >= 1 && v <= 65535) || msg

export const matches = (other: () => string, msg = 'Does not match') =>
  (v: string) => v === other() || msg

export function passwordStrength(password: string): 'weak' | 'medium' | 'strong' {
  if (!password || password.length < 8) return 'weak'
  let score = 0
  if (password.length >= 12) score++
  if (/[a-z]/.test(password) && /[A-Z]/.test(password)) score++
  if (/\d/.test(password)) score++
  if (/[^a-zA-Z0-9]/.test(password)) score++
  if (score <= 1) return 'weak'
  if (score <= 2) return 'medium'
  return 'strong'
}
