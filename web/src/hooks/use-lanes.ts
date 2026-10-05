import { useQuery } from "@tanstack/react-query"
import { api } from "@/lib/api"
import type { Lane } from "@/lib/types"
import { qk } from "@/hooks/query-keys"
import { useApiMutation } from "@/hooks/use-api-mutation"
import { useStreamLive } from "@/hooks/use-stream"

/** Lanes, kept fresh by the SSE stream; polls every 5 s when the stream is down. */
export function useLanes() {
  const live = useStreamLive()
  return useQuery({
    queryKey: qk.lanes,
    queryFn: api.lanes.list,
    refetchInterval: live ? false : 5_000,
    staleTime: live ? 30_000 : 0,
  })
}

export function useSetLaneEnabled() {
  return useApiMutation({
    mutationFn: ({ lane, enabled }: { lane: Lane; enabled: boolean }) => api.lanes.setEnabled(lane.id, enabled),
    invalidate: [qk.lanes, qk.overview],
    success: (_, { lane, enabled }) => `${lane.name} ${enabled ? "enabled" : "disabled"}`,
  })
}

export function useRestartLane() {
  return useApiMutation({
    mutationFn: (lane: Lane) => api.lanes.restart(lane.id),
    invalidate: [qk.lanes],
    success: (_, lane) => `Restarting ${lane.name}`,
  })
}

export function useRestartAllLanes() {
  return useApiMutation({
    mutationFn: () => api.lanes.restartAll(),
    invalidate: [qk.lanes],
    success: "Queued lanes for reconnection",
  })
}

export function useAddLanes() {
  return useApiMutation({
    mutationFn: (locations: string[]) => api.lanes.add(locations),
    invalidate: [qk.lanes, qk.overview, qk.surfshark],
    toastError: false,
    success: (_, locations) =>
      `Added ${locations.length} lane${locations.length === 1 ? "" : "s"} to the connection pool`,
  })
}

export function useRemoveLane() {
  return useApiMutation({
    mutationFn: (lane: Lane) => api.lanes.remove(lane.id),
    invalidate: [qk.lanes, qk.overview, qk.surfshark],
    success: (_, lane) => `Removed ${lane.name}`,
  })
}
