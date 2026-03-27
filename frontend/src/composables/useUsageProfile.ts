import { computed, ref } from 'vue'

import { getAccessConfig, updateAccessConfig } from '@/api/system'
import { normalizeUsageProfile, profileHomeRoute, type UsageProfile, usageProfileOptions } from '@/config/usage-profiles'

const STORAGE_KEY = 'zenith_usage_profile'

const usageProfile = ref<UsageProfile>(readStoredUsageProfile())
const usageProfileLoaded = ref(false)

let pendingLoad: Promise<UsageProfile> | null = null

function readStoredUsageProfile(): UsageProfile {
  if (typeof window === 'undefined') {
    return 'mixed'
  }

  return normalizeUsageProfile(window.localStorage.getItem(STORAGE_KEY))
}

function persistUsageProfile(nextProfile: UsageProfile) {
  usageProfile.value = nextProfile

  if (typeof window !== 'undefined') {
    window.localStorage.setItem(STORAGE_KEY, nextProfile)
  }
}

function syncUsageProfile(rawProfile?: string | null) {
  const normalized = normalizeUsageProfile(rawProfile)
  persistUsageProfile(normalized)
  usageProfileLoaded.value = true
  return normalized
}

export function useUsageProfile() {
  async function loadUsageProfile(force = false) {
    if (!force && usageProfileLoaded.value) {
      return usageProfile.value
    }

    if (!force && pendingLoad) {
      return pendingLoad
    }

    pendingLoad = (async () => {
      try {
        const res = await getAccessConfig() as { data?: { usage_profile?: string } }
        return syncUsageProfile(res?.data?.usage_profile)
      } catch {
        const fallback = readStoredUsageProfile()
        return syncUsageProfile(fallback)
      } finally {
        pendingLoad = null
      }
    })()

    return pendingLoad
  }

  async function saveUsageProfile(nextProfile: UsageProfile | string) {
    const normalized = normalizeUsageProfile(nextProfile)
    await updateAccessConfig({ usage_profile: normalized })
    syncUsageProfile(normalized)
    return normalized
  }

  return {
    usageProfile,
    usageProfileLoaded,
    currentProfileOption: computed(() => {
      return usageProfileOptions.find((option) => option.value === usageProfile.value) ?? usageProfileOptions[usageProfileOptions.length - 1]!
    }),
    homeRoute: computed(() => profileHomeRoute(usageProfile.value)),
    loadUsageProfile,
    saveUsageProfile,
    syncUsageProfile,
    readStoredUsageProfile,
  }
}
