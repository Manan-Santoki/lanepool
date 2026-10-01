import { useQuery } from "@tanstack/react-query"
import { api } from "@/lib/api"
import type { ProxyUser, ProxyUserInput } from "@/lib/types"
import { qk } from "@/hooks/query-keys"
import { useApiMutation } from "@/hooks/use-api-mutation"

export function useUsers() {
  return useQuery({ queryKey: qk.users, queryFn: api.users.list, refetchInterval: 30_000 })
}

export function useCreateUser() {
  return useApiMutation({
    mutationFn: (input: ProxyUserInput) => api.users.create(input),
    invalidate: [qk.users, qk.overview],
    toastError: false,
    success: (res) => `Created ${res.user.username}`,
  })
}

export function useUpdateUser() {
  return useApiMutation({
    mutationFn: ({ id, input }: { id: number; input: ProxyUserInput }) => api.users.update(id, input),
    invalidate: [qk.users, qk.overview],
    toastError: false,
    success: (u) => `Saved ${u.username}`,
  })
}

export function useToggleUser() {
  return useApiMutation({
    mutationFn: ({ user, enabled }: { user: ProxyUser; enabled: boolean }) => api.users.update(user.id, { enabled }),
    invalidate: [qk.users, qk.overview],
    success: (u) => `${u.username} ${u.enabled ? "enabled" : "disabled"}`,
  })
}

export function useResetUserPassword() {
  return useApiMutation({
    mutationFn: ({ id, password }: { id: number; password?: string }) => api.users.resetPassword(id, password),
  })
}

export function useResetUsage() {
  return useApiMutation({
    mutationFn: (user: ProxyUser) => api.users.resetUsage(user.id),
    invalidate: [qk.users],
    success: (u) => `Usage reset for ${u.username}`,
  })
}

export function useDeleteUser() {
  return useApiMutation({
    mutationFn: (user: ProxyUser) => api.users.remove(user.id),
    invalidate: [qk.users, qk.overview],
    success: (_, u) => `Deleted ${u.username}`,
  })
}
