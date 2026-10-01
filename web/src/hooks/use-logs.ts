import { useInfiniteQuery } from "@tanstack/react-query"
import { api, type ConnLogFilters, type EventFilters } from "@/lib/api"
import { qk } from "@/hooks/query-keys"

const PAGE_SIZE = 100

export function useConnectionLogs(filters: ConnLogFilters) {
  return useInfiniteQuery({
    queryKey: qk.connLogs(filters),
    queryFn: ({ pageParam, signal }) => api.logs.connections({ ...filters, limit: PAGE_SIZE, cursor: pageParam }, signal),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last) => last.nextCursor || undefined,
    placeholderData: (prev) => prev,
  })
}

export function useEvents(filters: EventFilters) {
  return useInfiniteQuery({
    queryKey: qk.events(filters),
    queryFn: ({ pageParam, signal }) => api.logs.events({ ...filters, limit: PAGE_SIZE, cursor: pageParam }, signal),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last) => last.nextCursor || undefined,
    placeholderData: (prev) => prev,
  })
}
