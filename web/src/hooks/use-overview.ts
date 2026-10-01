import { useQuery } from "@tanstack/react-query"
import { api } from "@/lib/api"
import { qk } from "@/hooks/query-keys"

export function useOverview() {
  return useQuery({ queryKey: qk.overview, queryFn: api.overview, refetchInterval: 15_000 })
}
