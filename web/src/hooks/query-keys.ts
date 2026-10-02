import type { ConnLogFilters, EventFilters } from "@/lib/api"
import type { AnalyticsRange, TopBy } from "@/lib/types"

export const qk = {
  setup: ["setup"] as const,
  me: ["me"] as const,
  overview: ["overview"] as const,
  lanes: ["lanes"] as const,
  connections: ["connections"] as const,
  users: ["users"] as const,
  connLogs: (filters: ConnLogFilters) => ["logs", "connections", filters] as const,
  events: (filters: EventFilters) => ["logs", "events", filters] as const,
  traffic: (range: AnalyticsRange) => ["analytics", "traffic", range] as const,
  top: (range: AnalyticsRange, by: TopBy) => ["analytics", "top", range, by] as const,
  burned: ["burned"] as const,
  surfshark: ["providers", "surfshark"] as const,
  surfsharkLocations: ["providers", "surfshark", "locations"] as const,
  surfsharkAccount: ["providers", "surfshark", "account"] as const,
  wireguard: ["providers", "wireguard"] as const,
  channels: ["alerts", "channels"] as const,
  rules: ["alerts", "rules"] as const,
  settings: ["settings"] as const,
  admins: ["admins"] as const,
  tokens: ["tokens"] as const,
}
