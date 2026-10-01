import { useQuery } from "@tanstack/react-query"
import { api } from "@/lib/api"
import type { Admin, AdminInput, AdminPatch, ApiToken, SettingsInput, TokenScope } from "@/lib/types"
import { qk } from "@/hooks/query-keys"
import { useApiMutation } from "@/hooks/use-api-mutation"

export function useSettings() {
  return useQuery({ queryKey: qk.settings, queryFn: api.settings.get, staleTime: 30_000 })
}

export function useSaveSettings() {
  return useApiMutation({
    mutationFn: (input: SettingsInput) => api.settings.update(input),
    invalidate: [qk.settings, qk.overview],
    toastError: false,
    success: "Settings saved",
  })
}

export function useSetMaintenance() {
  return useApiMutation({
    mutationFn: (paused: boolean) => api.gateway.setMaintenance(paused),
    invalidate: [qk.settings, qk.overview],
    success: (_, paused) => (paused ? "Maintenance mode on" : "Maintenance mode off"),
  })
}

export function useRestartGateway() {
  return useApiMutation({
    mutationFn: () => api.gateway.restart(),
    invalidate: [qk.overview, qk.connections],
    success: "Proxy server restarting",
  })
}

export function useAdmins(enabled = true) {
  return useQuery({ queryKey: qk.admins, queryFn: api.admins.list, enabled })
}

export function useCreateAdmin() {
  return useApiMutation({
    mutationFn: (input: AdminInput) => api.admins.create(input),
    invalidate: [qk.admins],
    toastError: false,
    success: (a) => `Added ${a.email}`,
  })
}

export function useUpdateAdmin() {
  return useApiMutation({
    mutationFn: ({ admin, patch }: { admin: Admin; patch: AdminPatch }) => api.admins.update(admin.id, patch),
    invalidate: [qk.admins],
    success: (a) => `Updated ${a.email}`,
  })
}

export function useDeleteAdmin() {
  return useApiMutation({
    mutationFn: (admin: Admin) => api.admins.remove(admin.id),
    invalidate: [qk.admins],
    success: (_, a) => `Removed ${a.email}`,
  })
}

export function useTokens(enabled = true) {
  return useQuery({ queryKey: qk.tokens, queryFn: api.tokens.list, enabled })
}

export function useCreateToken() {
  return useApiMutation({
    mutationFn: (input: { name: string; scopes: TokenScope[]; expiresAt?: string }) => api.tokens.create(input),
    invalidate: [qk.tokens],
    toastError: false,
  })
}

export function useRevokeToken() {
  return useApiMutation({
    mutationFn: (t: ApiToken) => api.tokens.remove(t.id),
    invalidate: [qk.tokens],
    success: (_, t) => `Revoked "${t.name}"`,
  })
}
