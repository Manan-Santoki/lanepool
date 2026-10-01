export const SETTINGS_TABS = ["engine", "app", "admins", "tokens", "account"] as const
export type SettingsTab = (typeof SETTINGS_TABS)[number]
