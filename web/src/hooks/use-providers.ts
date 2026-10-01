import { useQuery } from "@tanstack/react-query"
import { api } from "@/lib/api"
import type { SurfsharkKey, SurfsharkSelection, WireguardConfig, WireguardInput } from "@/lib/types"
import { qk } from "@/hooks/query-keys"
import { useApiMutation } from "@/hooks/use-api-mutation"

export function useSurfshark() {
  return useQuery({ queryKey: qk.surfshark, queryFn: api.surfshark.get, refetchInterval: 30_000 })
}

export function useSurfsharkLocations(enabled = true) {
  return useQuery({ queryKey: qk.surfsharkLocations, queryFn: api.surfshark.locations, staleTime: 5 * 60_000, enabled })
}

export function useAddSurfsharkKey() {
  return useApiMutation({
    mutationFn: api.surfshark.addKey,
    invalidate: [qk.surfshark, qk.lanes],
    toastError: false,
    success: "Key added",
  })
}

export function useUpdateSurfsharkKey() {
  return useApiMutation({
    mutationFn: ({ key, ...body }: { key: SurfsharkKey; enabled?: boolean; label?: string }) =>
      api.surfshark.updateKey(key.id, body),
    invalidate: [qk.surfshark, qk.lanes],
    success: (k) => `Key "${k.label}" ${k.enabled ? "enabled" : "disabled"}`,
  })
}

export function useDeleteSurfsharkKey() {
  return useApiMutation({
    mutationFn: (key: SurfsharkKey) => api.surfshark.removeKey(key.id),
    invalidate: [qk.surfshark, qk.lanes],
    success: "Key deleted",
  })
}

export function useSaveSurfsharkSelection() {
  return useApiMutation({
    mutationFn: (sel: SurfsharkSelection) => api.surfshark.setSelection(sel),
    invalidate: [qk.surfshark, qk.lanes, qk.overview],
    toastError: false,
    success: "Lane selection saved",
  })
}

export function useWireguard() {
  return useQuery({ queryKey: qk.wireguard, queryFn: api.wireguard.list })
}

export function useAddWireguard() {
  return useApiMutation({
    mutationFn: (input: WireguardInput) => api.wireguard.create(input),
    invalidate: [qk.wireguard, qk.lanes],
    toastError: false,
    success: (c) => `Added ${c.name}`,
  })
}

export function useUpdateWireguard() {
  return useApiMutation({
    mutationFn: ({ config, ...body }: { config: WireguardConfig; enabled?: boolean }) =>
      api.wireguard.update(config.id, body),
    invalidate: [qk.wireguard, qk.lanes],
    success: (c) => `${c.name} ${c.enabled ? "enabled" : "disabled"}`,
  })
}

export function useDeleteWireguard() {
  return useApiMutation({
    mutationFn: (config: WireguardConfig) => api.wireguard.remove(config.id),
    invalidate: [qk.wireguard, qk.lanes],
    success: "Config deleted",
  })
}
