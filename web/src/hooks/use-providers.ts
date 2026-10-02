import { useQuery } from "@tanstack/react-query"
import { api } from "@/lib/api"
import type { SurfsharkKey, SurfsharkSelection, WireguardConfig, WireguardInput } from "@/lib/types"
import { qk } from "@/hooks/query-keys"
import { useApiMutation } from "@/hooks/use-api-mutation"

export function useSurfshark() {
  return useQuery({
    queryKey: qk.surfshark,
    queryFn: api.surfshark.get,
    refetchInterval: 30_000,
  })
}

export function useSurfsharkLocations(enabled = true) {
  return useQuery({
    queryKey: qk.surfsharkLocations,
    queryFn: api.surfshark.locations,
    staleTime: 5 * 60_000,
    enabled,
  })
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

export function useSurfsharkAccount() {
  return useQuery({
    queryKey: qk.surfsharkAccount,
    queryFn: api.surfshark.account,
    refetchInterval: 60_000,
  })
}

const accountKeys = [qk.surfsharkAccount, qk.surfshark, qk.lanes]

export function useConnectSurfsharkAccount() {
  return useApiMutation({
    mutationFn: api.surfshark.connectAccount,
    invalidate: accountKeys,
    toastError: false,
    success: "Surfshark account connected",
  })
}

export function useUpdateSurfsharkAccount() {
  return useApiMutation({
    mutationFn: api.surfshark.updateAccount,
    invalidate: accountKeys,
    toastError: false,
    success: "Key management saved",
  })
}

export function useDisconnectSurfsharkAccount() {
  return useApiMutation({
    mutationFn: () => api.surfshark.disconnectAccount(),
    invalidate: accountKeys,
    success: "Surfshark account disconnected",
  })
}

export function useGenerateSurfsharkKeys() {
  return useApiMutation({
    mutationFn: (count: number) => api.surfshark.generateKeys(count),
    invalidate: accountKeys,
    success: (keys) => `Generated ${keys.length} key${keys.length === 1 ? "" : "s"}`,
  })
}

export function useRotateSurfsharkKey() {
  return useApiMutation({
    mutationFn: (key: SurfsharkKey) => api.surfshark.rotateKey(key.id),
    invalidate: accountKeys,
    success: (_, key) => `Rotated "${key.label}"; its lanes reconnect on the new key`,
  })
}

export function useDeleteRemoteKey() {
  return useApiMutation({
    mutationFn: (id: string) => api.surfshark.deleteRemoteKey(id),
    invalidate: accountKeys,
    success: "Key deleted at Surfshark",
  })
}
