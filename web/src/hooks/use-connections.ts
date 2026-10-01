import { useQuery } from "@tanstack/react-query"
import { api } from "@/lib/api"
import { qk } from "@/hooks/query-keys"
import { useApiMutation } from "@/hooks/use-api-mutation"

export function useLiveConnections(paused = false) {
  return useQuery({
    queryKey: qk.connections,
    queryFn: api.connections.list,
    refetchInterval: paused ? false : 2_000,
    refetchIntervalInBackground: false,
  })
}

export function useKickConnections() {
  return useApiMutation({
    mutationFn: api.connections.kick,
    invalidate: [qk.connections, qk.overview],
    success: (res) => `Closed ${res.kicked} connection${res.kicked === 1 ? "" : "s"}`,
  })
}
