import { keepPreviousData, useQuery } from "@tanstack/react-query"
import { api } from "@/lib/api"
import type { AnalyticsRange, TopBy } from "@/lib/types"
import { qk } from "@/hooks/query-keys"

export function useTraffic(range: AnalyticsRange) {
  return useQuery({
    queryKey: qk.traffic(range),
    queryFn: () => api.analytics.traffic(range),
    placeholderData: keepPreviousData,
    refetchInterval: range === "24h" ? 60_000 : false,
  })
}

export function useTop(range: AnalyticsRange, by: TopBy, limit = 10) {
  return useQuery({
    queryKey: qk.top(range, by),
    queryFn: () => api.analytics.top(range, by, limit),
    placeholderData: keepPreviousData,
  })
}
