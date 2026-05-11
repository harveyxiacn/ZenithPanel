<script setup lang="ts">
import { ref, onMounted, computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { KeyIcon, LockClosedIcon, FingerPrintIcon, ShieldCheckIcon, ArrowPathIcon, ArrowDownTrayIcon, GlobeAltIcon, Cog6ToothIcon, BoltIcon, CpuChipIcon, AdjustmentsHorizontalIcon, TrashIcon } from '@heroicons/vue/24/outline'
import { checkForUpdate, applyUpdate, changePassword, get2FAStatus, setup2FA, verify2FA, disable2FA, getTLSStatus, uploadTLSCerts, removeTLS, getAccessConfig, updateAccessConfig, restartPanel, getCFProtectionStatus, enableCFProtection, disableCFProtection, getBBRStatus, enableBBR, disableBBR, getSwapStatus, createSwap, removeSwap, getSysctlStatus, enableSysctl, disableSysctl, getCleanupInfo, runCleanup, downloadBackup, restoreBackup, getDNSSettings, updateDNSSettings } from '@/api/system'
import { useConfirm } from '@/composables/useConfirm'
import { useToast } from '../composables/useToast'
import { useUsageProfile } from '@/composables/useUsageProfile'
import { usageProfileOptions, type UsageProfile, type UsageProfileOptionTone } from '@/config/usage-profiles'

const { t } = useI18n()
const { confirm: confirmDialog } = useConfirm()
const toast = useToast()
const { usageProfile, loadUsageProfile, syncUsageProfile } = useUsageProfile()

// ---- Change Password ----
const showPasswordForm = ref(false)
const oldPassword = ref('')
const newPassword = ref('')
const confirmNewPassword = ref('')
const passwordChanging = ref(false)
const passwordMsg = ref('')
const passwordMsgType = ref<'success' | 'error'>('success')

async function onChangePassword() {
  passwordMsg.value = ''
  if (!oldPassword.value || !newPassword.value) {
    passwordMsg.value = t('security.auth.errorEmpty')
    passwordMsgType.value = 'error'
    return
  }
  if (newPassword.value !== confirmNewPassword.value) {
    passwordMsg.value = t('security.auth.errorMismatch')
    passwordMsgType.value = 'error'
    return
  }
  if (newPassword.value.length < 8) {
    passwordMsg.value = t('security.auth.errorShort')
    passwordMsgType.value = 'error'
    return
  }
  passwordChanging.value = true
  try {
    const res = await changePassword(oldPassword.value, newPassword.value) as any
    if (res.code === 200) {
      passwordMsg.value = t('security.auth.passwordChanged')
      passwordMsgType.value = 'success'
      oldPassword.value = ''
      newPassword.value = ''
      confirmNewPassword.value = ''
      showPasswordForm.value = false
      toast.success(t('common.saved'))
    } else {
      passwordMsg.value = res.msg || 'Failed'
      passwordMsgType.value = 'error'
    }
  } catch (e: any) {
    passwordMsg.value = e?.response?.data?.msg || 'Failed to change password'
    passwordMsgType.value = 'error'
    toast.error(passwordMsg.value)
  }
  passwordChanging.value = false
}

// ---- 2FA ----
const twoFAEnabled = ref(false)
const twoFALoading = ref(false)
const twoFAStep = ref<'idle' | 'setup' | 'verify' | 'codes'>('idle')
const twoFAQR = ref('')
const twoFASecret = ref('')
const twoFARecoveryCodes = ref<string[]>([])
const twoFACode = ref('')
const twoFAMsg = ref('')
const twoFADisablePassword = ref('')
const showDisable2FA = ref(false)
const codesSaved = ref(false)

async function load2FAStatus() {
  try {
    const res = await get2FAStatus() as any
    if (res.code === 200) twoFAEnabled.value = res.data.enabled
  } catch { toast.error(t('common.errorOccurred')) }
}

async function onSetup2FA() {
  twoFALoading.value = true
  twoFAMsg.value = ''
  try {
    const res = await setup2FA() as any
    if (res.code === 200) {
      twoFAQR.value = res.data.qr_base64
      twoFASecret.value = res.data.secret
      twoFARecoveryCodes.value = res.data.recovery_codes
      twoFAStep.value = 'setup'
    } else {
      twoFAMsg.value = res.msg || 'Failed'
    }
  } catch (e: any) {
    twoFAMsg.value = e?.response?.data?.msg || 'Failed'
    toast.error(twoFAMsg.value)
  }
  twoFALoading.value = false
}

async function onVerify2FA() {
  if (!twoFACode.value) return
  twoFALoading.value = true
  twoFAMsg.value = ''
  try {
    const res = await verify2FA(twoFACode.value) as any
    if (res.code === 200) {
      twoFAStep.value = 'codes'
      twoFAEnabled.value = true
    } else {
      twoFAMsg.value = res.msg || 'Invalid code'
    }
  } catch (e: any) {
    twoFAMsg.value = e?.response?.data?.msg || 'Invalid code'
    toast.error(twoFAMsg.value)
  }
  twoFALoading.value = false
}

async function onDisable2FA() {
  if (!twoFADisablePassword.value) return
  twoFALoading.value = true
  twoFAMsg.value = ''
  try {
    const res = await disable2FA(twoFADisablePassword.value) as any
    if (res.code === 200) {
      twoFAEnabled.value = false
      showDisable2FA.value = false
      twoFADisablePassword.value = ''
      twoFAStep.value = 'idle'
    } else {
      twoFAMsg.value = res.msg || 'Failed'
    }
  } catch (e: any) {
    twoFAMsg.value = e?.response?.data?.msg || 'Failed'
    toast.error(twoFAMsg.value)
  }
  twoFALoading.value = false
}

function finish2FASetup() {
  twoFAStep.value = 'idle'
  twoFACode.value = ''
  codesSaved.value = false
}

// ---- TLS ----
const tlsEnabled = ref(false)
const tlsLoading = ref(false)
const tlsMsg = ref('')
const tlsMsgType = ref<'success' | 'error'>('success')
const certFile = ref<File | null>(null)
const keyFile = ref<File | null>(null)
const showCFGuide = ref(false)

async function loadTLSStatus() {
  try {
    const res = await getTLSStatus() as any
    if (res.code === 200) tlsEnabled.value = res.data.enabled
  } catch { toast.error(t('common.errorOccurred')) }
}

async function onUploadTLS() {
  if (!certFile.value || !keyFile.value) {
    tlsMsg.value = t('security.tls.selectFiles')
    tlsMsgType.value = 'error'
    return
  }
  tlsLoading.value = true
  tlsMsg.value = ''
  const fd = new FormData()
  fd.append('cert', certFile.value)
  fd.append('key', keyFile.value)
  try {
    const res = await uploadTLSCerts(fd) as any
    if (res.code === 200) {
      tlsMsg.value = res.msg
      tlsMsgType.value = 'success'
      tlsEnabled.value = true
      toast.success(t('common.saved'))
    } else {
      tlsMsg.value = res.msg || 'Upload failed'
      tlsMsgType.value = 'error'
    }
  } catch (e: any) {
    tlsMsg.value = e?.response?.data?.msg || 'Upload failed'
    tlsMsgType.value = 'error'
    toast.error(tlsMsg.value)
  }
  tlsLoading.value = false
}

async function onRemoveTLS() {
  const ok = await confirmDialog({
    title: t('common.confirm'),
    message: t('security.confirmRemoveTLS'),
    confirmText: t('common.confirm'),
    variant: 'danger',
  })
  if (!ok) return
  tlsLoading.value = true
  try {
    const res = await removeTLS() as any
    if (res.code === 200) {
      tlsEnabled.value = false
      tlsMsg.value = res.msg
      tlsMsgType.value = 'success'
      toast.success(t('common.deleted'))
    }
  } catch (e: any) {
    tlsMsg.value = e?.response?.data?.msg || 'Failed'
    tlsMsgType.value = 'error'
    toast.error(tlsMsg.value)
  }
  tlsLoading.value = false
}

// ---- Access Configuration ----
const accessPath = ref('')
const accessPort = ref('')
const accessUsageProfile = ref<UsageProfile>('mixed')
const accessIPWhitelist = ref('')
const accessYourIP = ref('')
const accessLoading = ref(false)

// ---- DNS Settings (Sing-box / Xray outbound DNS) ----
const dnsMode = ref('plain')
const dnsPrimary = ref('')
const dnsSecondary = ref('')
const dnsLoading = ref(false)

async function loadDNSSettings() {
  try {
    const res = await getDNSSettings() as any
    if (res.code === 200) {
      dnsMode.value = res.data.dns_mode || 'plain'
      dnsPrimary.value = res.data.dns_primary || ''
      dnsSecondary.value = res.data.dns_secondary || ''
    }
  } catch { /* silent */ }
}

async function onSaveDNS() {
  dnsLoading.value = true
  try {
    await updateDNSSettings({
      dns_mode: dnsMode.value,
      dns_primary: dnsPrimary.value,
      dns_secondary: dnsSecondary.value,
    })
    toast.success('DNS settings saved. Re-apply proxy config to take effect.')
  } catch (e: any) {
    toast.error(e?.response?.data?.msg || 'Failed to save DNS settings')
  } finally {
    dnsLoading.value = false
  }
}
const accessMsg = ref('')
const accessMsgType = ref<'success' | 'error'>('success')

function profileToneClasses(tone: UsageProfileOptionTone, selected: boolean) {
  if (!selected) {
    return 'border-slate-200 bg-white hover:border-primary-300'
  }

  switch (tone) {
    case 'emerald':
      return 'border-emerald-300 bg-emerald-50 shadow-[0_0_0_1px_rgba(52,211,153,0.25)]'
    case 'sky':
      return 'border-sky-300 bg-sky-50 shadow-[0_0_0_1px_rgba(56,189,248,0.25)]'
    default:
      return 'border-amber-300 bg-amber-50 shadow-[0_0_0_1px_rgba(251,191,36,0.25)]'
  }
}

async function loadAccessConfig() {
  try {
    await loadUsageProfile()
    const res = await getAccessConfig() as any
    if (res.code === 200) {
      accessPath.value = res.data.panel_path || ''
      accessPort.value = res.data.port || ''
      accessOriginalPort.value = res.data.port || ''
      accessUsageProfile.value = usageProfile.value
      accessIPWhitelist.value = res.data.ip_whitelist || ''
      accessYourIP.value = res.data.your_ip || ''
    }
  } catch { toast.error(t('common.errorOccurred')) }
}

const accessRestarting = ref(false)
const accessOriginalPort = ref('')

async function onSaveAccess() {
  accessLoading.value = true
  accessMsg.value = ''
  try {
    const res = await updateAccessConfig({
      panel_path: accessPath.value,
      port: accessPort.value,
      usage_profile: accessUsageProfile.value,
      ip_whitelist: accessIPWhitelist.value,
    }) as any
    if (res.code === 200) {
      syncUsageProfile(accessUsageProfile.value)
      accessMsg.value = res.msg
      accessMsgType.value = 'success'
      toast.success(t('common.saved'))
    } else {
      accessMsg.value = res.msg || 'Failed'
      accessMsgType.value = 'error'
    }
  } catch (e: any) {
    accessMsg.value = e?.response?.data?.msg || 'Failed to save'
    accessMsgType.value = 'error'
    toast.error(accessMsg.value)
  }
  accessLoading.value = false
}

const accessNeedsRestart = computed(() => accessPort.value !== accessOriginalPort.value && accessPort.value !== '')

async function onRestartPanel() {
  const newPort = accessPort.value
  const newPath = accessPath.value
  const ok = await confirmDialog({
    title: t('common.confirm'),
    message: t('security.access.confirmRestart'),
    confirmText: t('common.confirm'),
    variant: 'warning',
  })
  if (!ok) return
  accessRestarting.value = true
  accessMsg.value = ''
  try {
    // Save first, then restart
    await updateAccessConfig({ panel_path: newPath, port: newPort })
    const res = await restartPanel() as any
    if (res.code === 200) {
      let countdown = 10
      const timer = setInterval(() => {
        countdown--
        accessMsg.value = t('security.update.restarting', { n: countdown })
        if (countdown <= 0) {
          clearInterval(timer)
          // Build new URL with potentially new port
          const proto = window.location.protocol
          const host = window.location.hostname
          const path = newPath ? '/' + newPath + '/' : '/'
          const newUrl = `${proto}//${host}:${newPort}${path}`
          const poll = setInterval(async () => {
            try {
              const r = await fetch(`${proto}//${host}:${newPort}/api/v1/ping`, { signal: AbortSignal.timeout(3000) })
              if (r.ok) { clearInterval(poll); window.location.href = newUrl }
            } catch { /* still restarting */ }
          }, 2000)
        }
      }, 1000)
    } else {
      accessMsg.value = res.msg || 'Restart failed'
      accessMsgType.value = 'error'
      accessRestarting.value = false
    }
  } catch (e: any) {
    accessMsg.value = e?.response?.data?.msg || 'Restart failed'
    accessMsgType.value = 'error'
    toast.error(accessMsg.value)
    accessRestarting.value = false
  }
}

// ---- Cloudflare Protection ----
const cfEnabled = ref(false)
const cfLoading = ref(false)
const cfMsg = ref('')
const cfMsgType = ref<'success' | 'error'>('success')
const cfPort = ref('')

async function loadCFStatus() {
  try {
    const res = await getCFProtectionStatus() as any
    if (res.code === 200) {
      cfEnabled.value = res.data.enabled
      cfPort.value = res.data.port
    }
  } catch { toast.error(t('common.errorOccurred')) }
}

async function onToggleCF() {
  cfLoading.value = true
  cfMsg.value = ''
  try {
    const res = cfEnabled.value
      ? await disableCFProtection() as any
      : await enableCFProtection() as any
    if (res.code === 200) {
      cfEnabled.value = !cfEnabled.value
      cfMsg.value = res.msg
      cfMsgType.value = 'success'
      toast.success(t('common.applied'))
    } else {
      cfMsg.value = res.msg || 'Failed'
      cfMsgType.value = 'error'
    }
  } catch (e: any) {
    cfMsg.value = e?.response?.data?.msg || 'Failed'
    cfMsgType.value = 'error'
    toast.error(cfMsg.value)
  }
  cfLoading.value = false
}

// ---- Update ----
const updateChecking = ref(false)
const updateAvailable = ref(false)
const updateApplying = ref(false)
const currentImageID = ref('')
const latestImageID = ref('')
const updateError = ref('')
const updateChecked = ref(false)

async function onCheckUpdate() {
  updateChecking.value = true
  updateError.value = ''
  try {
    const res = await checkForUpdate() as any
    if (res.code === 200 && res.data) {
      updateAvailable.value = res.data.available
      currentImageID.value = res.data.current_id
      latestImageID.value = res.data.latest_id
      updateChecked.value = true
    } else {
      updateError.value = res.msg || 'Check failed'
    }
  } catch (e: any) {
    updateError.value = e?.response?.data?.msg || 'Failed to check for updates'
    toast.error(updateError.value)
  }
  updateChecking.value = false
}

async function onApplyUpdate() {
  const ok = await confirmDialog({
    title: t('common.confirm'),
    message: t('security.update.confirmRestart'),
    confirmText: t('common.confirm'),
    variant: 'warning',
  })
  if (!ok) return
  updateApplying.value = true
  updateError.value = ''
  try {
    const res = await applyUpdate() as any
    if (res.code === 200) {
      let countdown = 10
      updateError.value = ''
      const timer = setInterval(() => {
        countdown--
        updateError.value = t('security.update.restarting', { n: countdown })
        if (countdown <= 0) {
          clearInterval(timer)
          const poll = setInterval(async () => {
            try {
              const r = await fetch('/api/v1/ping', { signal: AbortSignal.timeout(3000) })
              if (r.ok) { clearInterval(poll); window.location.reload() }
            } catch { /* still restarting */ }
          }, 2000)
        }
      }, 1000)
    } else {
      updateError.value = res.msg || 'Update failed'
      updateApplying.value = false
    }
  } catch (e: any) {
    updateError.value = e?.response?.data?.msg || 'Update request failed'
    toast.error(updateError.value)
    updateApplying.value = false
  }
}

// ---- BBR ----
const bbrEnabled = ref(false)
const bbrCurrent = ref('')
const bbrAvailable = ref('')
const bbrLoading = ref(false)
const bbrMsg = ref('')
const bbrMsgType = ref<'success' | 'error'>('success')

async function loadBBRStatus() {
  try {
    const res = await getBBRStatus() as any
    if (res.code === 200) {
      bbrEnabled.value = res.data.enabled
      bbrCurrent.value = res.data.current
      bbrAvailable.value = res.data.available
    }
  } catch { toast.error(t('common.errorOccurred')) }
}

async function onToggleBBR() {
  bbrLoading.value = true
  bbrMsg.value = ''
  try {
    const res = bbrEnabled.value
      ? await disableBBR() as any
      : await enableBBR() as any
    if (res.code === 200) {
      bbrMsg.value = res.msg
      bbrMsgType.value = 'success'
      toast.success(t('common.applied'))
      await loadBBRStatus()
    } else {
      bbrMsg.value = res.msg || 'Failed'
      bbrMsgType.value = 'error'
    }
  } catch (e: any) {
    bbrMsg.value = e?.response?.data?.msg || 'Failed'
    bbrMsgType.value = 'error'
    toast.error(bbrMsg.value)
  }
  bbrLoading.value = false
}

// ---- Swap ----
const swapEnabled = ref(false)
const swapTotalMB = ref(0)
const swapUsedMB = ref(0)
const swapFilePath = ref('')
const swapLoading = ref(false)
const swapMsg = ref('')
const swapMsgType = ref<'success' | 'error'>('success')
const swapSizeMB = ref(1024)

async function loadSwapStatus() {
  try {
    const res = await getSwapStatus() as any
    if (res.code === 200) {
      swapEnabled.value = res.data.enabled
      swapTotalMB.value = res.data.total_mb
      swapUsedMB.value = res.data.used_mb
      swapFilePath.value = res.data.file_path
    }
  } catch { toast.error(t('common.errorOccurred')) }
}

async function onCreateSwap() {
  swapLoading.value = true
  swapMsg.value = ''
  try {
    const res = await createSwap(swapSizeMB.value) as any
    if (res.code === 200) {
      swapMsg.value = res.msg
      swapMsgType.value = 'success'
      toast.success(t('common.created'))
      await loadSwapStatus()
    } else {
      swapMsg.value = res.msg || 'Failed'
      swapMsgType.value = 'error'
    }
  } catch (e: any) {
    swapMsg.value = e?.response?.data?.msg || 'Failed'
    swapMsgType.value = 'error'
    toast.error(swapMsg.value)
  }
  swapLoading.value = false
}

async function onRemoveSwap() {
  const ok = await confirmDialog({
    title: t('common.confirm'),
    message: t('security.confirmRemoveSwap'),
    confirmText: t('common.confirm'),
    variant: 'warning',
  })
  if (!ok) return
  swapLoading.value = true
  swapMsg.value = ''
  try {
    const res = await removeSwap() as any
    if (res.code === 200) {
      swapMsg.value = res.msg
      swapMsgType.value = 'success'
      toast.success(t('common.deleted'))
      await loadSwapStatus()
    } else {
      swapMsg.value = res.msg || 'Failed'
      swapMsgType.value = 'error'
    }
  } catch (e: any) {
    swapMsg.value = e?.response?.data?.msg || 'Failed'
    swapMsgType.value = 'error'
    toast.error(swapMsg.value)
  }
  swapLoading.value = false
}

// ---- Sysctl Network Tuning ----
const sysctlEnabled = ref(false)
const sysctlLoading = ref(false)
const sysctlMsg = ref('')
const sysctlMsgType = ref<'success' | 'error'>('success')

async function loadSysctlStatus() {
  try {
    const res = await getSysctlStatus() as any
    if (res.code === 200) {
      sysctlEnabled.value = res.data.enabled
    }
  } catch { toast.error(t('common.errorOccurred')) }
}

async function onToggleSysctl() {
  sysctlLoading.value = true
  sysctlMsg.value = ''
  try {
    const res = sysctlEnabled.value
      ? await disableSysctl() as any
      : await enableSysctl() as any
    if (res.code === 200) {
      sysctlMsg.value = res.msg
      sysctlMsgType.value = 'success'
      toast.success(t('common.applied'))
      await loadSysctlStatus()
    } else {
      sysctlMsg.value = res.msg || 'Failed'
      sysctlMsgType.value = 'error'
    }
  } catch (e: any) {
    sysctlMsg.value = e?.response?.data?.msg || 'Failed'
    sysctlMsgType.value = 'error'
    toast.error(sysctlMsg.value)
  }
  sysctlLoading.value = false
}

// ---- System Cleanup ----
const cleanupInfo = ref<any>(null)
const cleanupResult = ref<any>(null)
const cleanupScanning = ref(false)
const cleanupRunning = ref(false)
const cleanupMsg = ref('')
const cleanupMsgType = ref<'success' | 'error'>('success')

async function onScanCleanup() {
  cleanupScanning.value = true
  cleanupResult.value = null
  try {
    const res = await getCleanupInfo() as any
    if (res.code === 200) cleanupInfo.value = res.data
  } catch { toast.error(t('common.errorOccurred')) }
  cleanupScanning.value = false
}

async function onRunCleanup() {
  const ok = await confirmDialog({
    title: t('common.confirm'),
    message: t('optimize.cleanup.confirmRun'),
    confirmText: t('common.confirm'),
    variant: 'warning',
  })
  if (!ok) return
  cleanupRunning.value = true
  cleanupMsg.value = ''
  try {
    const res = await runCleanup() as any
    if (res.code === 200) {
      cleanupResult.value = res.data
      cleanupMsg.value = res.msg
      cleanupMsgType.value = 'success'
      toast.success(t('common.applied'))
      cleanupInfo.value = null
    } else {
      cleanupMsg.value = res.msg || 'Failed'
      cleanupMsgType.value = 'error'
    }
  } catch (e: any) {
    cleanupMsg.value = e?.response?.data?.msg || 'Failed'
    cleanupMsgType.value = 'error'
    toast.error(cleanupMsg.value)
  }
  cleanupRunning.value = false
}

// ---- Backup / Restore ----
const backupExporting = ref(false)
const backupRestoring = ref(false)
const backupMsg = ref('')
const backupMsgType = ref<'success' | 'error'>('success')
const restoreInputRef = ref<HTMLInputElement | null>(null)

async function onExportBackup() {
  backupExporting.value = true
  backupMsg.value = ''
  try {
    const res = await downloadBackup() as any
    const blob = res instanceof Blob ? res : res.data
    if (!(blob instanceof Blob)) {
      throw new Error('Unexpected response body')
    }
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    const stamp = new Date().toISOString().replace(/[-:]/g, '').slice(0, 15)
    a.download = `zenithpanel-backup-${stamp}.zip`
    document.body.appendChild(a)
    a.click()
    document.body.removeChild(a)
    URL.revokeObjectURL(url)
    toast.success(t('security.backup.exported'))
    backupMsg.value = t('security.backup.exported')
    backupMsgType.value = 'success'
  } catch (e: any) {
    backupMsg.value = e?.response?.data?.msg || e?.message || 'Failed'
    backupMsgType.value = 'error'
    toast.error(backupMsg.value)
  }
  backupExporting.value = false
}

async function onRestoreFileChosen(ev: Event) {
  const input = ev.target as HTMLInputElement
  const file = input.files?.[0]
  input.value = ''
  if (!file) return
  const ok = await confirmDialog({
    title: t('security.backup.confirmTitle'),
    message: t('security.backup.confirmRestore'),
    confirmText: t('security.backup.restore'),
    variant: 'warning',
  })
  if (!ok) return
  backupRestoring.value = true
  backupMsg.value = ''
  try {
    const res = await restoreBackup(file) as any
    if (res.code === 200) {
      backupMsg.value = t('security.backup.restored')
      backupMsgType.value = 'success'
      toast.success(backupMsg.value)
    } else {
      backupMsg.value = res.msg || 'Failed'
      backupMsgType.value = 'error'
      toast.error(backupMsg.value)
    }
  } catch (e: any) {
    backupMsg.value = e?.response?.data?.msg || e?.message || 'Failed'
    backupMsgType.value = 'error'
    toast.error(backupMsg.value)
  }
  backupRestoring.value = false
}

// ---- Port Security ----
const panelPort = ref(window.location.port || (window.location.protocol === 'https:' ? '443' : '80'))
const cloudflarePorts = [443, 2053, 2083, 2087, 2096, 8443]
const scannedPorts = [80, 8080, 8888, 3000, 5000]
const portNumber = computed(() => Number(panelPort.value))
const isCFPort = computed(() => cloudflarePorts.includes(portNumber.value))
const isCommonPort = computed(() => scannedPorts.includes(portNumber.value))

// ---- Notifications ----
const notifyConfig = ref({
  notify_telegram_token: '',
  notify_telegram_chat_id: '',
  notify_webhook_url: '',
  notify_enable_expiring_soon: 'false',
  notify_enable_expired: 'false',
  notify_enable_traffic_limit: 'false',
  notify_enable_proxy_crashed: 'false',
})
const notifySaving = ref(false)
const notifyTestLoading = ref<'telegram' | 'webhook' | null>(null)

async function loadNotifyConfig() {
  try {
    const res = await fetch('/api/v1/admin/notify', {
      headers: { Authorization: `Bearer ${localStorage.getItem('token') || ''}` }
    })
    const data = await res.json()
    if (data.code === 200 && data.data) {
      Object.assign(notifyConfig.value, data.data)
    }
  } catch { /* silent */ }
}

async function saveNotifyConfig() {
  notifySaving.value = true
  try {
    const res = await fetch('/api/v1/admin/notify', {
      method: 'PUT',
      headers: {
        Authorization: `Bearer ${localStorage.getItem('token') || ''}`,
        'Content-Type': 'application/json',
      },
      body: JSON.stringify(notifyConfig.value),
    })
    const data = await res.json()
    if (data.code === 200) toast.success(t('common.saved'))
    else toast.error(data.msg || t('common.errorOccurred'))
  } catch { toast.error(t('common.errorOccurred')) }
  notifySaving.value = false
}

async function testNotify(channel: 'telegram' | 'webhook') {
  notifyTestLoading.value = channel
  try {
    const payload: Record<string, string> = { channel }
    if (channel === 'telegram') {
      payload.token = notifyConfig.value.notify_telegram_token
      payload.chat_id = notifyConfig.value.notify_telegram_chat_id
    } else {
      payload.url = notifyConfig.value.notify_webhook_url
    }
    const res = await fetch('/api/v1/admin/notify/test', {
      method: 'POST',
      headers: {
        Authorization: `Bearer ${localStorage.getItem('token') || ''}`,
        'Content-Type': 'application/json',
      },
      body: JSON.stringify(payload),
    })
    const data = await res.json()
    if (data.code === 200) toast.success('Test notification sent')
    else toast.error(data.msg || 'Test failed')
  } catch { toast.error(t('common.errorOccurred')) }
  notifyTestLoading.value = null
}

onMounted(() => {
  load2FAStatus()
  loadTLSStatus()
  loadAccessConfig()
  loadCFStatus()
  loadBBRStatus()
  loadSwapStatus()
  loadSysctlStatus()
  loadNotifyConfig()
  loadDNSSettings()
})
</script>

<template>
  <div class="py-2">
    <!-- Header -->
    <div class="mb-8">
      <h1 class="text-3xl font-bold text-slate-800 tracking-tight">{{ $t('security.title') }}</h1>
      <p class="text-slate-500 mt-1">{{ $t('security.subtitle') }}</p>
    </div>

    <div class="grid grid-cols-1 lg:grid-cols-3 gap-8">
      <!-- Left Column: Settings -->
      <div class="lg:col-span-2 space-y-6">

        <!-- Panel Update -->
        <div class="bg-white rounded-2xl shadow-sm border border-slate-100 overflow-hidden">
          <div class="p-6 border-b border-slate-100 flex items-center">
            <div class="bg-emerald-500/10 text-emerald-500 p-2 rounded-lg mr-4">
              <ArrowDownTrayIcon class="h-6 w-6" />
            </div>
            <div>
              <h3 class="text-lg font-medium text-slate-800">{{ $t('security.update.title') }}</h3>
              <p class="text-sm text-slate-500">{{ $t('security.update.subtitle') }}</p>
            </div>
          </div>
          <div class="p-6 space-y-4">
            <div v-if="updateChecked" class="space-y-3">
              <div class="flex items-center justify-between text-sm">
                <span class="text-slate-500">{{ $t('security.update.currentImage') }}</span>
                <code class="bg-slate-100 px-2 py-0.5 rounded text-xs text-slate-700">{{ currentImageID }}</code>
              </div>
              <div class="flex items-center justify-between text-sm">
                <span class="text-slate-500">{{ $t('security.update.latestImage') }}</span>
                <code class="bg-slate-100 px-2 py-0.5 rounded text-xs text-slate-700">{{ latestImageID }}</code>
              </div>
              <div v-if="updateAvailable" class="bg-amber-50 border border-amber-200 rounded-lg p-3 text-sm text-amber-700">
                {{ $t('security.update.available') }}
              </div>
              <div v-else class="bg-emerald-50 border border-emerald-200 rounded-lg p-3 text-sm text-emerald-700">
                {{ $t('security.update.upToDate') }}
              </div>
            </div>
            <div v-if="updateError" class="bg-rose-50 border border-rose-200 rounded-lg p-3 text-sm text-rose-700">
              {{ updateError }}
            </div>
            <div class="flex items-center space-x-3 pt-2">
              <button @click="onCheckUpdate" :disabled="updateChecking || updateApplying" class="flex items-center bg-slate-100 hover:bg-slate-200 disabled:opacity-50 text-slate-700 px-4 py-2 rounded-lg text-sm font-medium transition">
                <ArrowPathIcon class="h-4 w-4 mr-2" :class="{ 'animate-spin': updateChecking }" />
                {{ updateChecking ? $t('security.update.checking') : $t('security.update.checkForUpdates') }}
              </button>
              <button v-if="updateAvailable" @click="onApplyUpdate" :disabled="updateApplying" class="flex items-center bg-primary-600 hover:bg-primary-700 disabled:opacity-50 text-white px-4 py-2 rounded-lg text-sm font-medium transition">
                <ArrowDownTrayIcon class="h-4 w-4 mr-2" />
                {{ updateApplying ? $t('security.update.updating') : $t('security.update.updateNow') }}
              </button>
            </div>
          </div>
        </div>

        <!-- HTTPS / TLS Configuration -->
        <div class="bg-white rounded-2xl shadow-sm border border-slate-100 overflow-hidden">
          <div class="p-6 border-b border-slate-100 flex items-center justify-between">
            <div class="flex items-center">
              <div class="bg-blue-500/10 text-blue-500 p-2 rounded-lg mr-4">
                <GlobeAltIcon class="h-6 w-6" />
              </div>
              <div>
                <h3 class="text-lg font-medium text-slate-800">{{ $t('security.tls.title') }}</h3>
                <p class="text-sm text-slate-500">{{ $t('security.tls.subtitle') }}</p>
              </div>
            </div>
            <span :class="['px-2.5 py-0.5 rounded-full text-xs font-medium', tlsEnabled ? 'bg-emerald-100 text-emerald-700' : 'bg-amber-100 text-amber-700']">
              {{ tlsEnabled ? $t('security.tls.statusEnabled') : $t('security.tls.statusDisabled') }}
            </span>
          </div>
          <div class="p-6 space-y-4">
            <!-- Upload Certs -->
            <div class="space-y-3">
              <div>
                <label class="block text-sm font-medium text-slate-700 mb-1">{{ $t('security.tls.uploadCert') }}</label>
                <input type="file" accept=".pem,.crt,.cer" @change="certFile = ($event.target as HTMLInputElement).files?.[0] || null" class="w-full text-sm text-slate-500 file:mr-3 file:py-1.5 file:px-3 file:rounded-lg file:border-0 file:text-sm file:font-medium file:bg-slate-100 file:text-slate-700 hover:file:bg-slate-200" />
              </div>
              <div>
                <label class="block text-sm font-medium text-slate-700 mb-1">{{ $t('security.tls.uploadKey') }}</label>
                <input type="file" accept=".pem,.key" @change="keyFile = ($event.target as HTMLInputElement).files?.[0] || null" class="w-full text-sm text-slate-500 file:mr-3 file:py-1.5 file:px-3 file:rounded-lg file:border-0 file:text-sm file:font-medium file:bg-slate-100 file:text-slate-700 hover:file:bg-slate-200" />
              </div>
              <div class="flex items-center space-x-3">
                <button @click="onUploadTLS" :disabled="tlsLoading" class="bg-primary-600 hover:bg-primary-700 disabled:opacity-50 text-white px-4 py-2 rounded-lg text-sm font-medium transition">
                  {{ $t('security.tls.uploadBtn') }}
                </button>
                <button v-if="tlsEnabled" @click="onRemoveTLS" :disabled="tlsLoading" class="bg-rose-100 hover:bg-rose-200 text-rose-700 px-4 py-2 rounded-lg text-sm font-medium transition">
                  {{ $t('security.tls.removeBtn') }}
                </button>
              </div>
              <p class="text-xs text-slate-400">{{ $t('security.tls.restartNote') }}</p>
            </div>

            <div v-if="tlsMsg" :class="['text-sm p-2 rounded-lg', tlsMsgType === 'success' ? 'bg-emerald-50 text-emerald-700' : 'bg-rose-50 text-rose-700']">{{ tlsMsg }}</div>

            <!-- Cloudflare Guide -->
            <div class="border-t border-slate-100 pt-4">
              <button @click="showCFGuide = !showCFGuide" class="flex items-center text-sm font-medium text-blue-600 hover:text-blue-700">
                <span class="mr-1">{{ showCFGuide ? '▼' : '▶' }}</span>
                {{ $t('security.tls.cfGuideTitle') }}
              </button>
              <div v-if="showCFGuide" class="mt-3 space-y-2 text-sm text-slate-600 bg-slate-50 rounded-lg p-4">
                <p class="font-medium text-slate-700">{{ $t('security.tls.cfGuideIntro') }}</p>
                <ol class="list-decimal list-inside space-y-1.5">
                  <li>{{ $t('security.tls.cfStep1') }}</li>
                  <li>{{ $t('security.tls.cfStep2') }}</li>
                  <li>{{ $t('security.tls.cfStep3') }}</li>
                  <li>{{ $t('security.tls.cfStep4') }}</li>
                  <li>{{ $t('security.tls.cfStep5') }}</li>
                </ol>
                <p class="text-xs text-slate-400 mt-2">{{ $t('security.tls.cfNote') }}</p>
              </div>
            </div>
          </div>
        </div>

        <!-- Access Configuration -->
        <div class="bg-white rounded-2xl shadow-sm border border-slate-100 overflow-hidden">
          <div class="p-6 border-b border-slate-100 flex items-center">
            <div class="bg-violet-500/10 text-violet-500 p-2 rounded-lg mr-4">
              <Cog6ToothIcon class="h-6 w-6" />
            </div>
            <div>
              <h3 class="text-lg font-medium text-slate-800">{{ $t('security.access.title') }}</h3>
              <p class="text-sm text-slate-500">{{ $t('security.access.subtitle') }}</p>
            </div>
          </div>
          <div class="p-6 space-y-4">
            <div>
              <label class="block text-sm font-medium text-slate-700 mb-2">{{ $t('security.access.usageProfile') }}</label>
              <div class="grid grid-cols-1 gap-3">
                <button
                  v-for="option in usageProfileOptions"
                  :key="option.value"
                  type="button"
                  @click="accessUsageProfile = option.value"
                  :class="[
                    'text-left rounded-xl border px-4 py-3 transition-all duration-200',
                    profileToneClasses(option.tone, accessUsageProfile === option.value)
                  ]"
                >
                  <p class="font-medium text-slate-800">{{ $t(option.labelKey) }}</p>
                  <p class="text-sm text-slate-500 mt-1">{{ $t(option.descriptionKey) }}</p>
                  <p class="text-xs text-slate-400 mt-2">{{ $t(option.emphasisKey) }}</p>
                </button>
              </div>
              <p class="text-xs text-slate-400 mt-2">{{ $t('security.access.usageProfileHint') }}</p>
            </div>
            <div>
              <label class="block text-sm font-medium text-slate-700 mb-1">{{ $t('security.access.panelPort') }}</label>
              <div class="flex items-center space-x-2">
                <input v-model="accessPort" type="text" inputmode="numeric" maxlength="5" class="w-32 border border-slate-300 rounded-lg px-3 py-2 text-sm focus:border-primary-500 focus:ring-primary-500" />
                <span class="text-xs text-slate-400">{{ $t('security.access.portHint') }}</span>
              </div>
            </div>
            <div>
              <label class="block text-sm font-medium text-slate-700 mb-1">{{ $t('security.access.securityPath') }}</label>
              <div class="flex items-center space-x-2">
                <span class="text-sm text-slate-400">/</span>
                <input v-model="accessPath" type="text" :placeholder="'my-secret-path'" class="flex-1 border border-slate-300 rounded-lg px-3 py-2 text-sm focus:border-primary-500 focus:ring-primary-500" />
              </div>
              <p class="text-xs text-slate-400 mt-1">{{ $t('security.access.pathHint') }}</p>
            </div>

            <!-- IP Whitelist -->
            <div class="border-t border-slate-100 pt-4">
              <label class="block text-sm font-medium text-slate-700 mb-1">IP Whitelist</label>
              <p class="text-xs text-slate-500 mb-2">Comma-separated IPs or CIDRs allowed to reach the panel. Leave empty to allow all. Non-matching requests get 404 (panel is hidden).</p>
              <textarea v-model="accessIPWhitelist" rows="2" placeholder="1.2.3.4, 10.0.0.0/24"
                class="w-full border border-slate-300 rounded-lg px-3 py-2 text-sm font-mono focus:border-primary-500 focus:ring-primary-500"></textarea>
              <div class="flex items-start gap-2 mt-2 text-xs">
                <span class="text-slate-500">Your current IP:</span>
                <span class="font-mono text-slate-700">{{ accessYourIP || '—' }}</span>
                <button v-if="accessYourIP" @click="accessIPWhitelist = accessIPWhitelist ? accessIPWhitelist + ',' + accessYourIP : accessYourIP"
                  class="text-primary-600 hover:underline">+ Add to whitelist</button>
              </div>
              <p v-if="accessIPWhitelist.trim()" class="text-xs text-amber-700 bg-amber-50 border border-amber-200 rounded p-2 mt-2">
                ⚠ Whitelist is active. If your IP changes and isn't whitelisted, you'll be locked out.
              </p>
            </div>

            <div v-if="accessMsg" :class="['text-sm p-2 rounded-lg', accessMsgType === 'success' ? 'bg-emerald-50 text-emerald-700' : 'bg-rose-50 text-rose-700']">{{ accessMsg }}</div>
            <div class="flex items-center space-x-3">
              <button @click="onSaveAccess" :disabled="accessLoading || accessRestarting" class="bg-primary-600 hover:bg-primary-700 disabled:opacity-50 text-white px-4 py-2 rounded-lg text-sm font-medium transition">
                {{ accessLoading ? $t('common.loading') : $t('common.save') }}
              </button>
              <button v-if="accessNeedsRestart" @click="onRestartPanel" :disabled="accessRestarting" class="bg-amber-500 hover:bg-amber-600 disabled:opacity-50 text-white px-4 py-2 rounded-lg text-sm font-medium transition">
                {{ accessRestarting ? $t('security.update.restarting', { n: '...' }) : $t('security.access.applyRestart') }}
              </button>
            </div>
          </div>
        </div>

        <!-- DNS Settings -->
        <div class="bg-white rounded-2xl shadow-sm border border-slate-100 overflow-hidden">
          <div class="p-6 border-b border-slate-100 flex items-center">
            <div class="bg-sky-500/10 text-sky-500 p-2 rounded-lg mr-4">
              <GlobeAltIcon class="h-6 w-6" />
            </div>
            <div>
              <h3 class="text-lg font-medium text-slate-800">DNS Configuration</h3>
              <p class="text-sm text-slate-500">Controls how Sing-box / Xray resolve domains for outbound traffic</p>
            </div>
          </div>
          <div class="p-6 space-y-4">
            <div>
              <label class="block text-sm font-medium text-slate-700 mb-1">DNS Mode</label>
              <div class="flex gap-3">
                <label class="flex items-center gap-2 text-sm">
                  <input v-model="dnsMode" type="radio" value="plain" />
                  <span>Plain DNS (UDP)</span>
                </label>
                <label class="flex items-center gap-2 text-sm">
                  <input v-model="dnsMode" type="radio" value="doh" />
                  <span>DNS over HTTPS (DoH)</span>
                </label>
              </div>
              <p class="text-xs text-slate-400 mt-1">DoH hides DNS queries from your ISP. Plain DNS is faster but visible to network operators.</p>
            </div>
            <div class="grid grid-cols-1 md:grid-cols-2 gap-3">
              <div>
                <label class="text-xs font-medium text-slate-600">Primary (optional override)</label>
                <input v-model="dnsPrimary" type="text"
                  :placeholder="dnsMode === 'doh' ? 'https://cloudflare-dns.com/dns-query' : 'udp://8.8.8.8'"
                  class="w-full border border-slate-300 rounded-lg px-3 py-2 text-sm font-mono mt-1" />
              </div>
              <div>
                <label class="text-xs font-medium text-slate-600">Secondary (optional override)</label>
                <input v-model="dnsSecondary" type="text"
                  :placeholder="dnsMode === 'doh' ? 'https://dns.google/dns-query' : 'udp://1.1.1.1'"
                  class="w-full border border-slate-300 rounded-lg px-3 py-2 text-sm font-mono mt-1" />
              </div>
            </div>
            <p class="text-xs text-slate-500 bg-slate-50 border border-slate-200 rounded p-2">
              ℹ Re-apply your proxy config after saving for changes to take effect.
            </p>
            <button @click="onSaveDNS" :disabled="dnsLoading" class="bg-primary-600 hover:bg-primary-700 disabled:opacity-50 text-white px-4 py-2 rounded-lg text-sm font-medium transition">
              {{ dnsLoading ? $t('common.loading') : $t('common.save') }}
            </button>
          </div>
        </div>

        <!-- Cloudflare Protection -->
        <div class="bg-white rounded-2xl shadow-sm border border-slate-100 overflow-hidden">
          <div class="p-6 border-b border-slate-100 flex items-center justify-between">
            <div class="flex items-center">
              <div class="bg-orange-500/10 text-orange-500 p-2 rounded-lg mr-4">
                <ShieldCheckIcon class="h-6 w-6" />
              </div>
              <div>
                <h3 class="text-lg font-medium text-slate-800">{{ $t('security.cloudflare.title') }}</h3>
                <p class="text-sm text-slate-500">{{ $t('security.cloudflare.subtitle') }}</p>
              </div>
            </div>
            <span :class="['px-2.5 py-0.5 rounded-full text-xs font-medium', cfEnabled ? 'bg-emerald-100 text-emerald-700' : 'bg-slate-100 text-slate-500']">
              {{ cfEnabled ? $t('common.enabled') : $t('common.disabled') }}
            </span>
          </div>
          <div class="p-6 space-y-4">
            <p class="text-sm text-slate-600">{{ $t('security.cloudflare.desc') }}</p>
            <div class="bg-slate-50 rounded-lg p-3 text-xs text-slate-500 space-y-1">
              <p>{{ $t('security.cloudflare.howItWorks') }}</p>
              <ul class="list-disc list-inside space-y-0.5 ml-1">
                <li>{{ $t('security.cloudflare.step1', { port: cfPort || accessPort }) }}</li>
                <li>{{ $t('security.cloudflare.step2') }}</li>
              </ul>
            </div>
            <div v-if="cfMsg" :class="['text-sm p-2 rounded-lg', cfMsgType === 'success' ? 'bg-emerald-50 text-emerald-700' : 'bg-rose-50 text-rose-700']">{{ cfMsg }}</div>
            <button @click="onToggleCF" :disabled="cfLoading" :class="[
              'px-4 py-2 rounded-lg text-sm font-medium transition disabled:opacity-50',
              cfEnabled ? 'bg-rose-100 hover:bg-rose-200 text-rose-700' : 'bg-orange-500 hover:bg-orange-600 text-white'
            ]">
              {{ cfLoading ? $t('common.loading') : (cfEnabled ? $t('security.cloudflare.disable') : $t('security.cloudflare.enable')) }}
            </button>
          </div>
        </div>

        <!-- ====== System Optimization Section ====== -->
        <div class="pt-2">
          <h2 class="text-xl font-bold text-slate-800 tracking-tight mb-1">{{ $t('optimize.title') }}</h2>
          <p class="text-slate-500 text-sm mb-4">{{ $t('optimize.subtitle') }}</p>
        </div>

        <!-- BBR Congestion Control -->
        <div class="bg-white rounded-2xl shadow-sm border border-slate-100 overflow-hidden">
          <div class="p-6 border-b border-slate-100 flex items-center justify-between">
            <div class="flex items-center">
              <div class="bg-cyan-500/10 text-cyan-500 p-2 rounded-lg mr-4">
                <BoltIcon class="h-6 w-6" />
              </div>
              <div>
                <h3 class="text-lg font-medium text-slate-800">{{ $t('optimize.bbr.title') }}</h3>
                <p class="text-sm text-slate-500">{{ $t('optimize.bbr.subtitle') }}</p>
              </div>
            </div>
            <span :class="['px-2.5 py-0.5 rounded-full text-xs font-medium', bbrEnabled ? 'bg-emerald-100 text-emerald-700' : 'bg-slate-100 text-slate-500']">
              {{ bbrEnabled ? $t('common.enabled') : $t('common.disabled') }}
            </span>
          </div>
          <div class="p-6 space-y-4">
            <p class="text-sm text-slate-600">{{ $t('optimize.bbr.desc') }}</p>
            <div class="bg-slate-50 rounded-lg p-3 text-xs text-slate-500 space-y-1">
              <div class="flex items-center justify-between">
                <span>{{ $t('optimize.bbr.current') }}</span>
                <code class="bg-white px-2 py-0.5 rounded border border-slate-200 text-slate-700">{{ bbrCurrent || '—' }}</code>
              </div>
              <div class="flex items-center justify-between">
                <span>{{ $t('optimize.bbr.available') }}</span>
                <code class="bg-white px-2 py-0.5 rounded border border-slate-200 text-slate-700">{{ bbrAvailable || '—' }}</code>
              </div>
            </div>
            <p class="text-xs text-slate-400">{{ $t('optimize.bbr.kernelNote') }}</p>
            <div v-if="bbrMsg" :class="['text-sm p-2 rounded-lg', bbrMsgType === 'success' ? 'bg-emerald-50 text-emerald-700' : 'bg-rose-50 text-rose-700']">{{ bbrMsg }}</div>
            <button @click="onToggleBBR" :disabled="bbrLoading" :class="[
              'px-4 py-2 rounded-lg text-sm font-medium transition disabled:opacity-50',
              bbrEnabled ? 'bg-slate-100 hover:bg-slate-200 text-slate-700' : 'bg-cyan-500 hover:bg-cyan-600 text-white'
            ]">
              {{ bbrLoading ? $t('common.loading') : (bbrEnabled ? $t('optimize.bbr.disable') : $t('optimize.bbr.enable')) }}
            </button>
          </div>
        </div>

        <!-- Swap Memory -->
        <div class="bg-white rounded-2xl shadow-sm border border-slate-100 overflow-hidden">
          <div class="p-6 border-b border-slate-100 flex items-center justify-between">
            <div class="flex items-center">
              <div class="bg-purple-500/10 text-purple-500 p-2 rounded-lg mr-4">
                <CpuChipIcon class="h-6 w-6" />
              </div>
              <div>
                <h3 class="text-lg font-medium text-slate-800">{{ $t('optimize.swap.title') }}</h3>
                <p class="text-sm text-slate-500">{{ $t('optimize.swap.subtitle') }}</p>
              </div>
            </div>
            <span :class="['px-2.5 py-0.5 rounded-full text-xs font-medium', swapEnabled ? 'bg-emerald-100 text-emerald-700' : 'bg-slate-100 text-slate-500']">
              {{ swapEnabled ? $t('common.enabled') : $t('common.disabled') }}
            </span>
          </div>
          <div class="p-6 space-y-4">
            <p class="text-sm text-slate-600">{{ $t('optimize.swap.desc') }}</p>
            <div v-if="swapEnabled" class="bg-slate-50 rounded-lg p-3 text-xs text-slate-500 space-y-1">
              <div class="flex items-center justify-between">
                <span>{{ $t('optimize.swap.totalMb') }}</span>
                <code class="bg-white px-2 py-0.5 rounded border border-slate-200 text-slate-700">{{ swapTotalMB }} MB</code>
              </div>
              <div class="flex items-center justify-between">
                <span>{{ $t('optimize.swap.usedMb') }}</span>
                <code class="bg-white px-2 py-0.5 rounded border border-slate-200 text-slate-700">{{ swapUsedMB }} MB</code>
              </div>
              <div v-if="swapFilePath" class="flex items-center justify-between">
                <span>{{ $t('optimize.swap.filePath') }}</span>
                <code class="bg-white px-2 py-0.5 rounded border border-slate-200 text-slate-700">{{ swapFilePath }}</code>
              </div>
            </div>
            <div v-if="!swapEnabled" class="space-y-2">
              <label class="block text-sm font-medium text-slate-700">{{ $t('optimize.swap.sizeLabel') }}</label>
              <div class="flex items-center space-x-3">
                <select v-model="swapSizeMB" class="border border-slate-300 rounded-lg px-3 py-2 text-sm focus:border-primary-500 focus:ring-primary-500">
                  <option :value="256">256 MB</option>
                  <option :value="512">512 MB</option>
                  <option :value="1024">1 GB</option>
                  <option :value="2048">2 GB</option>
                  <option :value="4096">4 GB</option>
                </select>
              </div>
            </div>
            <div v-if="swapMsg" :class="['text-sm p-2 rounded-lg', swapMsgType === 'success' ? 'bg-emerald-50 text-emerald-700' : 'bg-rose-50 text-rose-700']">{{ swapMsg }}</div>
            <div class="flex items-center space-x-3">
              <button v-if="!swapEnabled" @click="onCreateSwap" :disabled="swapLoading" class="bg-purple-500 hover:bg-purple-600 disabled:opacity-50 text-white px-4 py-2 rounded-lg text-sm font-medium transition">
                {{ swapLoading ? $t('optimize.swap.creating') : $t('optimize.swap.create') }}
              </button>
              <button v-if="swapEnabled" @click="onRemoveSwap" :disabled="swapLoading" class="bg-rose-100 hover:bg-rose-200 text-rose-700 px-4 py-2 rounded-lg text-sm font-medium transition disabled:opacity-50">
                {{ swapLoading ? $t('common.loading') : $t('optimize.swap.remove') }}
              </button>
            </div>
          </div>
        </div>

        <!-- Network Tuning (sysctl) -->
        <div class="bg-white rounded-2xl shadow-sm border border-slate-100 overflow-hidden">
          <div class="p-6 border-b border-slate-100 flex items-center justify-between">
            <div class="flex items-center">
              <div class="bg-indigo-500/10 text-indigo-500 p-2 rounded-lg mr-4">
                <AdjustmentsHorizontalIcon class="h-6 w-6" />
              </div>
              <div>
                <h3 class="text-lg font-medium text-slate-800">{{ $t('optimize.sysctl.title') }}</h3>
                <p class="text-sm text-slate-500">{{ $t('optimize.sysctl.subtitle') }}</p>
              </div>
            </div>
            <span :class="['px-2.5 py-0.5 rounded-full text-xs font-medium', sysctlEnabled ? 'bg-emerald-100 text-emerald-700' : 'bg-slate-100 text-slate-500']">
              {{ sysctlEnabled ? $t('common.enabled') : $t('common.disabled') }}
            </span>
          </div>
          <div class="p-6 space-y-4">
            <p class="text-sm text-slate-600">{{ $t('optimize.sysctl.desc') }}</p>
            <div v-if="sysctlMsg" :class="['text-sm p-2 rounded-lg', sysctlMsgType === 'success' ? 'bg-emerald-50 text-emerald-700' : 'bg-rose-50 text-rose-700']">{{ sysctlMsg }}</div>
            <button @click="onToggleSysctl" :disabled="sysctlLoading" :class="[
              'px-4 py-2 rounded-lg text-sm font-medium transition disabled:opacity-50',
              sysctlEnabled ? 'bg-slate-100 hover:bg-slate-200 text-slate-700' : 'bg-indigo-500 hover:bg-indigo-600 text-white'
            ]">
              {{ sysctlLoading ? $t('common.loading') : (sysctlEnabled ? $t('optimize.sysctl.disable') : $t('optimize.sysctl.enable')) }}
            </button>
          </div>
        </div>

        <!-- System Cleanup -->
        <div class="bg-white rounded-2xl shadow-sm border border-slate-100 overflow-hidden">
          <div class="p-6 border-b border-slate-100 flex items-center">
            <div class="bg-amber-500/10 text-amber-500 p-2 rounded-lg mr-4">
              <TrashIcon class="h-6 w-6" />
            </div>
            <div>
              <h3 class="text-lg font-medium text-slate-800">{{ $t('optimize.cleanup.title') }}</h3>
              <p class="text-sm text-slate-500">{{ $t('optimize.cleanup.subtitle') }}</p>
            </div>
          </div>
          <div class="p-6 space-y-4">
            <p class="text-sm text-slate-600">{{ $t('optimize.cleanup.desc') }}</p>

            <!-- Scan results -->
            <div v-if="cleanupInfo" class="bg-slate-50 rounded-lg p-3 text-xs text-slate-500 space-y-1">
              <div class="flex items-center justify-between">
                <span>{{ $t('optimize.cleanup.journal') }}</span>
                <code class="bg-white px-2 py-0.5 rounded border border-slate-200 text-slate-700">{{ cleanupInfo.journal_size }}</code>
              </div>
              <div class="flex items-center justify-between">
                <span>{{ $t('optimize.cleanup.package') }}</span>
                <code class="bg-white px-2 py-0.5 rounded border border-slate-200 text-slate-700">{{ cleanupInfo.package_size }}</code>
              </div>
              <div class="flex items-center justify-between">
                <span>{{ $t('optimize.cleanup.docker') }}</span>
                <code class="bg-white px-2 py-0.5 rounded border border-slate-200 text-slate-700">{{ cleanupInfo.docker_size }}</code>
              </div>
            </div>

            <!-- Cleanup results -->
            <div v-if="cleanupResult" class="bg-emerald-50 border border-emerald-200 rounded-lg p-3 text-xs text-emerald-700 space-y-1">
              <div class="flex items-center justify-between">
                <span>{{ $t('optimize.cleanup.freedJournal') }}</span>
                <span class="font-medium">{{ cleanupResult.journal_freed }}</span>
              </div>
              <div class="flex items-center justify-between">
                <span>{{ $t('optimize.cleanup.freedPackage') }}</span>
                <span class="font-medium">{{ cleanupResult.package_freed }}</span>
              </div>
              <div class="flex items-center justify-between">
                <span>{{ $t('optimize.cleanup.freedDocker') }}</span>
                <span class="font-medium">{{ cleanupResult.docker_freed }}</span>
              </div>
            </div>

            <div v-if="cleanupMsg" :class="['text-sm p-2 rounded-lg', cleanupMsgType === 'success' ? 'bg-emerald-50 text-emerald-700' : 'bg-rose-50 text-rose-700']">{{ cleanupMsg }}</div>

            <div class="flex items-center space-x-3">
              <button @click="onScanCleanup" :disabled="cleanupScanning || cleanupRunning" class="bg-slate-100 hover:bg-slate-200 disabled:opacity-50 text-slate-700 px-4 py-2 rounded-lg text-sm font-medium transition">
                {{ cleanupScanning ? $t('optimize.cleanup.scanning') : $t('optimize.cleanup.scan') }}
              </button>
              <button @click="onRunCleanup" :disabled="cleanupRunning || cleanupScanning" class="bg-amber-500 hover:bg-amber-600 disabled:opacity-50 text-white px-4 py-2 rounded-lg text-sm font-medium transition">
                {{ cleanupRunning ? $t('optimize.cleanup.running') : $t('optimize.cleanup.run') }}
              </button>
            </div>
          </div>
        </div>

        <!-- Backup / Restore -->
        <div class="bg-white rounded-2xl shadow-sm border border-slate-100 overflow-hidden">
          <div class="p-6 border-b border-slate-100 flex items-center">
            <div class="bg-indigo-500/10 text-indigo-500 p-2 rounded-lg mr-4">
              <ArrowDownTrayIcon class="h-6 w-6" />
            </div>
            <div>
              <h3 class="text-lg font-medium text-slate-800">{{ $t('security.backup.title') }}</h3>
              <p class="text-sm text-slate-500">{{ $t('security.backup.subtitle') }}</p>
            </div>
          </div>
          <div class="p-6 space-y-4">
            <p class="text-sm text-slate-600">{{ $t('security.backup.desc') }}</p>
            <div v-if="backupMsg" :class="['text-sm p-2 rounded-lg', backupMsgType === 'success' ? 'bg-emerald-50 text-emerald-700' : 'bg-rose-50 text-rose-700']">{{ backupMsg }}</div>
            <div class="flex items-center space-x-3">
              <button @click="onExportBackup" :disabled="backupExporting || backupRestoring" class="bg-indigo-500 hover:bg-indigo-600 disabled:opacity-50 text-white px-4 py-2 rounded-lg text-sm font-medium transition">
                {{ backupExporting ? $t('security.backup.exporting') : $t('security.backup.export') }}
              </button>
              <button @click="restoreInputRef?.click()" :disabled="backupRestoring || backupExporting" class="bg-slate-100 hover:bg-slate-200 disabled:opacity-50 text-slate-700 px-4 py-2 rounded-lg text-sm font-medium transition">
                {{ backupRestoring ? $t('security.backup.restoring') : $t('security.backup.restore') }}
              </button>
              <input ref="restoreInputRef" type="file" accept=".zip,application/zip" class="hidden" @change="onRestoreFileChosen" />
            </div>
          </div>
        </div>

        <!-- Notification Settings -->
        <div class="bg-white rounded-2xl shadow-sm border border-slate-100 overflow-hidden">
          <div class="p-6 border-b border-slate-100 flex items-center">
            <div class="w-9 h-9 rounded-xl bg-amber-500/10 flex items-center justify-center mr-3">
              <BoltIcon class="h-5 w-5 text-amber-500" />
            </div>
            <div>
              <h2 class="text-base font-semibold text-slate-800">Notifications</h2>
              <p class="text-xs text-slate-500 mt-0.5">Telegram and Webhook alerts for expiring clients and traffic limits</p>
            </div>
          </div>
          <div class="p-6 space-y-5">
            <!-- Telegram -->
            <div>
              <h3 class="text-sm font-medium text-slate-700 mb-3 flex items-center gap-2">
                <span class="text-base">✈️</span> Telegram Bot
              </h3>
              <div class="grid grid-cols-2 gap-3 mb-2">
                <div>
                  <label class="text-xs font-medium text-slate-500">Bot Token</label>
                  <input v-model="notifyConfig.notify_telegram_token" type="password" placeholder="123456:ABC-DEF..." class="input-field text-sm mt-1 w-full" />
                </div>
                <div>
                  <label class="text-xs font-medium text-slate-500">Chat ID</label>
                  <input v-model="notifyConfig.notify_telegram_chat_id" placeholder="-1001234567890" class="input-field text-sm mt-1 w-full" />
                </div>
              </div>
              <button @click="testNotify('telegram')" :disabled="notifyTestLoading !== null || !notifyConfig.notify_telegram_token || !notifyConfig.notify_telegram_chat_id"
                class="text-xs bg-slate-100 hover:bg-slate-200 text-slate-700 px-3 py-1.5 rounded-lg disabled:opacity-50 transition">
                {{ notifyTestLoading === 'telegram' ? 'Sending…' : 'Send Test' }}
              </button>
            </div>

            <!-- Webhook -->
            <div>
              <h3 class="text-sm font-medium text-slate-700 mb-2 flex items-center gap-2">
                <span class="text-base">🔗</span> Webhook URL
              </h3>
              <div class="flex gap-2">
                <input v-model="notifyConfig.notify_webhook_url" placeholder="https://hooks.example.com/..." class="input-field text-sm flex-1" />
                <button @click="testNotify('webhook')" :disabled="notifyTestLoading !== null || !notifyConfig.notify_webhook_url"
                  class="text-xs bg-slate-100 hover:bg-slate-200 text-slate-700 px-3 py-1.5 rounded-lg disabled:opacity-50 transition whitespace-nowrap">
                  {{ notifyTestLoading === 'webhook' ? 'Sending…' : 'Test' }}
                </button>
              </div>
            </div>

            <!-- Event Toggles -->
            <div>
              <h3 class="text-sm font-medium text-slate-700 mb-3">Events</h3>
              <div class="space-y-2">
                <label v-for="item in [
                  { key: 'notify_enable_expiring_soon', label: 'Client expiring soon (within 3 days)' },
                  { key: 'notify_enable_expired', label: 'Client expired' },
                  { key: 'notify_enable_traffic_limit', label: 'Traffic usage >90%' },
                  { key: 'notify_enable_proxy_crashed', label: 'Proxy core crashed' },
                ]" :key="item.key" class="flex items-center gap-3">
                  <input type="checkbox" :checked="notifyConfig[item.key as keyof typeof notifyConfig] === 'true'"
                    @change="(e: any) => notifyConfig[item.key as keyof typeof notifyConfig] = e.target.checked ? 'true' : 'false'"
                    class="w-4 h-4 rounded accent-primary-600" />
                  <span class="text-sm text-slate-700">{{ item.label }}</span>
                </label>
              </div>
            </div>

            <button @click="saveNotifyConfig" :disabled="notifySaving"
              class="bg-primary-600 text-white text-sm px-5 py-2 rounded-lg hover:bg-primary-700 disabled:opacity-50 transition">
              {{ notifySaving ? $t('common.saving') : $t('common.save') }}
            </button>
          </div>
        </div>

        <!-- Authentication Settings -->
        <div class="bg-white rounded-2xl shadow-sm border border-slate-100 overflow-hidden">
          <div class="p-6 border-b border-slate-100 flex items-center">
            <div class="bg-rose-500/10 text-rose-500 p-2 rounded-lg mr-4">
              <KeyIcon class="h-6 w-6" />
            </div>
            <div>
              <h3 class="text-lg font-medium text-slate-800">{{ $t('security.auth.title') }}</h3>
              <p class="text-sm text-slate-500">{{ $t('security.auth.subtitle') }}</p>
            </div>
          </div>
          <div class="p-6 space-y-4">
            <!-- Password -->
            <div class="flex items-center justify-between">
              <div class="flex items-center">
                <LockClosedIcon class="h-5 w-5 text-slate-400 mr-3" />
                <div>
                  <h4 class="text-sm font-medium text-slate-900">{{ $t('security.auth.panelPassword') }}</h4>
                  <p class="text-xs text-slate-500">{{ $t('security.auth.panelPasswordDesc') }}</p>
                </div>
              </div>
              <button @click="showPasswordForm = !showPasswordForm" class="bg-slate-100 hover:bg-slate-200 text-slate-700 px-4 py-2 rounded-lg text-sm font-medium transition">{{ $t('common.change') }}</button>
            </div>

            <div v-if="showPasswordForm" class="pt-3 space-y-3">
              <input v-model="oldPassword" type="password" :placeholder="$t('security.auth.oldPassword')" class="w-full border border-slate-300 rounded-lg px-3 py-2 text-sm focus:border-primary-500 focus:ring-primary-500" />
              <input v-model="newPassword" type="password" :placeholder="$t('security.auth.newPasswordField')" class="w-full border border-slate-300 rounded-lg px-3 py-2 text-sm focus:border-primary-500 focus:ring-primary-500" />
              <input v-model="confirmNewPassword" type="password" :placeholder="$t('security.auth.confirmNewPassword')" class="w-full border border-slate-300 rounded-lg px-3 py-2 text-sm focus:border-primary-500 focus:ring-primary-500" />
              <div v-if="passwordMsg" :class="['text-sm p-2 rounded-lg', passwordMsgType === 'success' ? 'bg-emerald-50 text-emerald-700' : 'bg-rose-50 text-rose-700']">{{ passwordMsg }}</div>
              <button @click="onChangePassword" :disabled="passwordChanging" class="bg-primary-600 hover:bg-primary-700 disabled:opacity-50 text-white px-4 py-2 rounded-lg text-sm font-medium transition">
                {{ passwordChanging ? $t('common.loading') : $t('security.auth.savePassword') }}
              </button>
            </div>

            <!-- 2FA -->
            <div class="pt-4 border-t border-slate-100">
              <div class="flex items-center justify-between">
                <div class="flex items-center">
                  <FingerPrintIcon class="h-5 w-5 text-slate-400 mr-3" />
                  <div>
                    <h4 class="text-sm font-medium text-slate-900">{{ $t('security.auth.twoFactor') }}</h4>
                    <p class="text-xs" :class="twoFAEnabled ? 'text-emerald-500 font-medium' : 'text-slate-400'">
                      {{ twoFAEnabled ? $t('security.auth.twoFactorEnabled') : $t('security.auth.twoFactorDisabled') }}
                    </p>
                  </div>
                </div>
                <button v-if="!twoFAEnabled && twoFAStep === 'idle'" @click="onSetup2FA" :disabled="twoFALoading" class="bg-primary-600 hover:bg-primary-700 disabled:opacity-50 text-white px-4 py-2 rounded-lg text-sm font-medium transition">
                  {{ $t('security.auth.enable2fa') }}
                </button>
                <button v-else-if="twoFAEnabled && twoFAStep === 'idle'" @click="showDisable2FA = !showDisable2FA" class="bg-rose-100 hover:bg-rose-200 text-rose-700 px-4 py-2 rounded-lg text-sm font-medium transition">
                  {{ $t('security.auth.disable2fa') }}
                </button>
              </div>

              <!-- 2FA Setup Flow -->
              <div v-if="twoFAStep === 'setup'" class="mt-4 space-y-4 bg-slate-50 rounded-lg p-4">
                <p class="text-sm font-medium text-slate-700">{{ $t('security.auth.scanQR') }}</p>
                <div class="flex justify-center">
                  <img :src="'data:image/png;base64,' + twoFAQR" alt="QR Code" class="w-48 h-48 rounded-lg border border-slate-200" />
                </div>
                <div class="text-center">
                  <p class="text-xs text-slate-500 mb-1">{{ $t('security.auth.manualEntry') }}</p>
                  <code class="text-xs bg-white px-3 py-1 rounded border border-slate-200 select-all">{{ twoFASecret }}</code>
                </div>
                <div>
                  <label class="block text-sm font-medium text-slate-700 mb-1">{{ $t('security.auth.enterCode') }}</label>
                  <input v-model="twoFACode" type="text" inputmode="numeric" maxlength="6" class="w-full border border-slate-300 rounded-lg px-3 py-2 text-sm text-center tracking-widest" :placeholder="'000000'" />
                </div>
                <div v-if="twoFAMsg" class="text-sm p-2 rounded-lg bg-rose-50 text-rose-700">{{ twoFAMsg }}</div>
                <button @click="onVerify2FA" :disabled="twoFALoading || !twoFACode" class="w-full bg-primary-600 hover:bg-primary-700 disabled:opacity-50 text-white px-4 py-2 rounded-lg text-sm font-medium transition">
                  {{ $t('security.auth.verifyCode') }}
                </button>
              </div>

              <!-- Recovery Codes -->
              <div v-if="twoFAStep === 'codes'" class="mt-4 space-y-4 bg-amber-50 border border-amber-200 rounded-lg p-4">
                <p class="text-sm font-medium text-amber-800">{{ $t('security.auth.recoveryCodesTitle') }}</p>
                <p class="text-xs text-amber-700">{{ $t('security.auth.recoveryCodesDesc') }}</p>
                <div class="grid grid-cols-2 gap-2">
                  <code v-for="code in twoFARecoveryCodes" :key="code" class="bg-white px-3 py-1.5 rounded text-xs text-center font-mono border border-amber-200 select-all">{{ code }}</code>
                </div>
                <label class="flex items-center text-sm text-amber-800">
                  <input type="checkbox" v-model="codesSaved" class="mr-2 rounded border-amber-300" />
                  {{ $t('security.auth.recoveryCodesSaved') }}
                </label>
                <button @click="finish2FASetup" :disabled="!codesSaved" class="w-full bg-emerald-600 hover:bg-emerald-700 disabled:opacity-50 text-white px-4 py-2 rounded-lg text-sm font-medium transition">
                  {{ $t('common.done') }}
                </button>
              </div>

              <!-- Disable 2FA -->
              <div v-if="showDisable2FA" class="mt-4 space-y-3 bg-rose-50 rounded-lg p-4">
                <p class="text-sm text-rose-700">{{ $t('security.auth.confirmDisable2fa') }}</p>
                <input v-model="twoFADisablePassword" type="password" :placeholder="$t('security.auth.oldPassword')" class="w-full border border-rose-200 rounded-lg px-3 py-2 text-sm" />
                <div v-if="twoFAMsg" class="text-sm p-2 rounded-lg bg-rose-100 text-rose-700">{{ twoFAMsg }}</div>
                <button @click="onDisable2FA" :disabled="twoFALoading" class="bg-rose-600 hover:bg-rose-700 disabled:opacity-50 text-white px-4 py-2 rounded-lg text-sm font-medium transition">
                  {{ $t('security.auth.disable2fa') }}
                </button>
              </div>
            </div>
          </div>
        </div>

      </div>

      <!-- Right Column: Security Tips -->
      <div class="space-y-6">
        <div class="bg-white rounded-2xl shadow-sm border border-slate-100 overflow-hidden h-fit">
          <div class="p-6 border-b border-slate-100 flex items-center">
            <ShieldCheckIcon class="h-6 w-6 text-emerald-500 mr-2" />
            <h3 class="text-lg font-medium text-slate-800">{{ $t('security.tips.title') }}</h3>
          </div>
          <div class="p-6 space-y-4">
            <div class="text-sm text-slate-600 space-y-3">
              <p>{{ $t('security.tips.intro') }}</p>
              <ul class="list-disc list-inside space-y-2 text-slate-500">
                <li>{{ $t('security.tips.strongPassword') }}</li>
                <li>{{ $t('security.tips.enable2fa') }}</li>
                <li>{{ $t('security.tips.keepUpdated') }}</li>
                <li>{{ $t('security.tips.useHttps') }}</li>
                <li>{{ $t('security.tips.restrictApi') }}</li>
              </ul>
            </div>

            <!-- Port Security -->
            <div class="border-t border-slate-100 pt-4">
              <div class="flex items-center justify-between text-sm mb-2">
                <span class="text-slate-500">{{ $t('security.tips.portLabel') }}</span>
                <code class="bg-slate-100 px-2 py-0.5 rounded text-xs font-medium text-slate-700">{{ panelPort }}</code>
              </div>
              <div v-if="isCommonPort" class="bg-amber-50 border border-amber-200 rounded-lg p-3 text-xs text-amber-700">
                {{ $t('security.tips.portWarning') }}
              </div>
              <div v-else-if="isCFPort" class="bg-blue-50 border border-blue-200 rounded-lg p-3 text-xs text-blue-700">
                {{ $t('security.tips.portCF') }}
              </div>
              <div v-else class="bg-emerald-50 border border-emerald-200 rounded-lg p-3 text-xs text-emerald-700">
                {{ $t('security.tips.portGood') }}
              </div>
            </div>
          </div>
        </div>
      </div>

    </div>
  </div>
</template>
