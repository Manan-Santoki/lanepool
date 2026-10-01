import { useQuery } from "@tanstack/react-query"
import { api } from "@/lib/api"
import type { BurnInput, BurnedIp } from "@/lib/types"
import { qk } from "@/hooks/query-keys"
import { useApiMutation } from "@/hooks/use-api-mutation"

export function useBurned() {
  return useQuery({ queryKey: qk.burned, queryFn: api.burned.list, refetchInterval: 30_000 })
}

export function useCreateBurn() {
  return useApiMutation({
    mutationFn: (input: BurnInput) => api.burned.create(input),
    invalidate: [qk.burned],
    toastError: false,
    success: (b) => `Burned ${b.exitIp ?? b.laneName ?? b.laneId} for ${b.domain}`,
  })
}

export function useDeleteBurn() {
  return useApiMutation({
    mutationFn: (b: BurnedIp) => api.burned.remove(b.id),
    invalidate: [qk.burned],
    success: "Burned IP removed",
  })
}
