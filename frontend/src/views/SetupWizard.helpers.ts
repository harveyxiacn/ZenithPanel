import { normalizeUsageProfile } from '../config/usage-profiles'

export interface SetupWizardPayloadInput {
  adminUsername: string
  newPassword: string
  customPanelPath: string
  usageProfile: string
}

export function buildSetupPayload(form: SetupWizardPayloadInput) {
  return {
    username: form.adminUsername,
    password: form.newPassword,
    panel_path: form.customPanelPath,
    usage_profile: normalizeUsageProfile(form.usageProfile),
  }
}
