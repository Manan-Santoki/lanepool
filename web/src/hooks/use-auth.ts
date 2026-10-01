import { useQuery, useQueryClient } from "@tanstack/react-query"
import { api } from "@/lib/api"
import type { Admin } from "@/lib/types"
import { qk } from "@/hooks/query-keys"
import { useApiMutation } from "@/hooks/use-api-mutation"

export const meQueryOptions = {
  queryKey: qk.me,
  queryFn: async () => (await api.auth.me()).admin,
  staleTime: 60_000,
  retry: false,
} as const

export const setupQueryOptions = {
  queryKey: qk.setup,
  queryFn: api.setup.status,
  staleTime: Infinity,
  retry: 1,
} as const

/** The logged-in admin. Only used below the authenticated layout, where it is always loaded. */
export function useMe(): Admin | undefined {
  return useQuery(meQueryOptions).data
}

/** Viewers are read-only: hide or disable anything that mutates. */
export function useCanWrite(): boolean {
  return useMe()?.role === "admin"
}

export function useLogin() {
  const qc = useQueryClient()
  return useApiMutation({
    mutationFn: api.auth.login,
    toastError: false,
    onSuccess: (res) => qc.setQueryData(qk.me, res.admin),
  })
}

export function useSetup() {
  const qc = useQueryClient()
  return useApiMutation({
    mutationFn: api.setup.create,
    toastError: false,
    onSuccess: (res) => {
      qc.setQueryData(qk.setup, { needsSetup: false })
      qc.setQueryData(qk.me, res.admin)
    },
  })
}

export function useLogout() {
  const qc = useQueryClient()
  return useApiMutation({
    mutationFn: api.auth.logout,
    toastError: false,
    onSuccess: () => {
      qc.clear()
    },
  })
}

export function useChangePassword() {
  return useApiMutation({
    mutationFn: api.auth.changePassword,
    toastError: false,
    success: "Password changed",
  })
}
