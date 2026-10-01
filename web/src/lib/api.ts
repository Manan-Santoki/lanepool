import type {
  Admin,
  AdminEnvelope,
  AdminInput,
  AdminPatch,
  AlertChannel,
  AlertChannelInput,
  AlertRule,
  AlertRuleInput,
  AnalyticsRange,
  ApiToken,
  AppEvent,
  BurnInput,
  BurnedIp,
  ConnLog,
  CreateTokenResponse,
  CreateUserResponse,
  KickResponse,
  Lane,
  LiveConn,
  Overview,
  Page,
  PasswordResponse,
  ProxyUser,
  ProxyUserInput,
  SetupStatus,
  Settings,
  SettingsInput,
  SurfsharkKey,
  SurfsharkLocation,
  SurfsharkProvider,
  SurfsharkSelection,
  TokenScope,
  TopBy,
  TopItem,
  TrafficPoint,
  WireguardConfig,
  WireguardInput,
} from "@/lib/types"

/** Error thrown for every non-2xx API response (and for network failures, with status 0). */
export class ApiError extends Error {
  readonly status: number
  readonly fields: Record<string, string>

  constructor(status: number, message: string, fields?: Record<string, string>) {
    super(message)
    this.name = "ApiError"
    this.status = status
    this.fields = fields ?? {}
  }

  get isUnauthorized() {
    return this.status === 401
  }
  get isForbidden() {
    return this.status === 403
  }
}

export function isApiError(err: unknown): err is ApiError {
  return err instanceof ApiError
}

/** Human-readable message for any thrown value. */
export function errorMessage(err: unknown): string {
  if (err instanceof ApiError) return err.message
  if (err instanceof Error) return err.message
  return "Something went wrong"
}

/** Field errors from an ApiError, or an empty object. */
export function fieldErrors(err: unknown): Record<string, string> {
  return err instanceof ApiError ? err.fields : {}
}

type Unauthorized = () => void
let onUnauthorized: Unauthorized | null = null

/** Registered once by the app: called whenever an authenticated request gets a 401. */
export function setUnauthorizedHandler(fn: Unauthorized | null) {
  onUnauthorized = fn
}

// Endpoints where a 401 is an expected answer and must not trigger a redirect.
const NO_REDIRECT = new Set(["/api/auth/me", "/api/auth/login", "/api/setup"])

export type QueryValue = string | number | boolean | null | undefined
export type Query = Record<string, QueryValue>

/** Builds a query string, skipping empty values. Returns "" or "?a=b". */
export function toQueryString(query?: Query): string {
  if (!query) return ""
  const params = new URLSearchParams()
  for (const [key, value] of Object.entries(query)) {
    if (value === undefined || value === null || value === "") continue
    params.set(key, String(value))
  }
  const qs = params.toString()
  return qs ? `?${qs}` : ""
}

interface RequestOptions {
  query?: Query
  body?: unknown
  signal?: AbortSignal
}

async function request<T>(method: string, path: string, opts: RequestOptions = {}): Promise<T> {
  const url = `/api${path}${toQueryString(opts.query)}`
  const headers: Record<string, string> = { Accept: "application/json" }
  let body: string | undefined
  if (opts.body !== undefined) {
    headers["Content-Type"] = "application/json"
    body = JSON.stringify(opts.body)
  }

  let res: Response
  try {
    res = await fetch(url, { method, headers, body, credentials: "include", signal: opts.signal })
  } catch (err) {
    if (err instanceof DOMException && err.name === "AbortError") throw err
    throw new ApiError(0, "Cannot reach the lanepool server. Check your connection.")
  }

  if (!res.ok) {
    let message = res.statusText || `Request failed (${res.status})`
    let fields: Record<string, string> | undefined
    try {
      const data: unknown = await res.json()
      if (data && typeof data === "object") {
        const d = data as { error?: unknown; fields?: unknown }
        if (typeof d.error === "string" && d.error) message = d.error
        if (d.fields && typeof d.fields === "object") fields = d.fields as Record<string, string>
      }
    } catch {
      // Non-JSON error body; keep the status text.
    }
    if (res.status === 401 && !NO_REDIRECT.has(`/api${path}`)) onUnauthorized?.()
    throw new ApiError(res.status, message, fields)
  }

  if (res.status === 204 || res.status === 202) return undefined as T
  const text = await res.text()
  return (text ? JSON.parse(text) : undefined) as T
}

const get = <T>(path: string, query?: Query, signal?: AbortSignal) => request<T>("GET", path, { query, signal })
const post = <T>(path: string, body?: unknown) => request<T>("POST", path, { body })
const patch = <T>(path: string, body: unknown) => request<T>("PATCH", path, { body })
const put = <T>(path: string, body: unknown) => request<T>("PUT", path, { body })
const del = (path: string) => request<void>("DELETE", path)

const enc = encodeURIComponent

// ---- filters ------------------------------------------------------------------------

export interface ConnLogFilters {
  from?: string
  to?: string
  user?: string
  lane?: string
  result?: string
  client?: string
  q?: string
}

export interface EventFilters {
  from?: string
  to?: string
  level?: string
  type?: string
  lane?: string
  q?: string
}

// ---- endpoints ----------------------------------------------------------------------

export const api = {
  setup: {
    status: () => get<SetupStatus>("/setup"),
    create: (body: { email: string; name: string; password: string }) => post<AdminEnvelope>("/setup", body),
  },
  auth: {
    me: () => get<AdminEnvelope>("/auth/me"),
    login: (body: { email: string; password: string }) => post<AdminEnvelope>("/auth/login", body),
    logout: () => post<void>("/auth/logout"),
    changePassword: (body: { currentPassword: string; newPassword: string }) => post<void>("/auth/password", body),
  },
  overview: () => get<Overview>("/overview"),
  lanes: {
    list: () => get<Lane[]>("/lanes"),
    setEnabled: (id: string, enabled: boolean) => patch<Lane>(`/lanes/${enc(id)}`, { enabled }),
    restart: (id: string) => post<void>(`/lanes/${enc(id)}/restart`),
    restartAll: () => post<void>("/lanes/restart-all"),
    add: (locations: string[]) => post<Lane[]>("/lanes/add", { locations }),
    remove: (id: string) => del(`/lanes/${enc(id)}`),
  },
  gateway: {
    restart: () => post<void>("/gateway/restart"),
    setMaintenance: (paused: boolean) => post<void>("/gateway/maintenance", { paused }),
  },
  connections: {
    list: () => get<LiveConn[]>("/connections"),
    kick: (body: { ids?: string[]; userId?: number }) => post<KickResponse>("/connections/kick", body),
  },
  users: {
    list: () => get<ProxyUser[]>("/users"),
    get: (id: number) => get<ProxyUser>(`/users/${id}`),
    create: (body: ProxyUserInput) => post<CreateUserResponse>("/users", body),
    update: (id: number, body: ProxyUserInput) => patch<ProxyUser>(`/users/${id}`, body),
    resetPassword: (id: number, password?: string) =>
      post<PasswordResponse>(`/users/${id}/password`, password ? { password } : {}),
    resetUsage: (id: number) => post<ProxyUser>(`/users/${id}/reset-usage`),
    remove: (id: number) => del(`/users/${id}`),
  },
  logs: {
    connections: (filters: ConnLogFilters & { limit?: number; cursor?: string }, signal?: AbortSignal) =>
      get<Page<ConnLog>>("/logs/connections", { ...filters }, signal),
    /** Same-origin URL for the CSV export (a plain link; the cookie authenticates it). */
    connectionsCsvUrl: (filters: ConnLogFilters) => `/api/logs/connections.csv${toQueryString({ ...filters })}`,
    events: (filters: EventFilters & { limit?: number; cursor?: string }, signal?: AbortSignal) =>
      get<Page<AppEvent>>("/events", { ...filters }, signal),
  },
  analytics: {
    traffic: (range: AnalyticsRange, opts: { user?: string; lane?: string } = {}) =>
      get<TrafficPoint[]>("/analytics/traffic", { range, ...opts }),
    top: (range: AnalyticsRange, by: TopBy, limit?: number) => get<TopItem[]>("/analytics/top", { range, by, limit }),
  },
  burned: {
    list: () => get<BurnedIp[]>("/burned"),
    create: (body: BurnInput) => post<BurnedIp>("/burned", body),
    remove: (id: number) => del(`/burned/${id}`),
  },
  surfshark: {
    get: () => get<SurfsharkProvider>("/providers/surfshark"),
    addKey: (body: { privateKey: string; label?: string }) => post<SurfsharkKey>("/providers/surfshark/keys", body),
    updateKey: (id: number, body: { enabled?: boolean; label?: string }) =>
      patch<SurfsharkKey>(`/providers/surfshark/keys/${id}`, body),
    removeKey: (id: number) => del(`/providers/surfshark/keys/${id}`),
    setSelection: (body: SurfsharkSelection) => put<SurfsharkProvider>("/providers/surfshark/selection", body),
    locations: () => get<SurfsharkLocation[]>("/providers/surfshark/locations"),
  },
  wireguard: {
    list: () => get<WireguardConfig[]>("/providers/wireguard"),
    create: (body: WireguardInput) => post<WireguardConfig>("/providers/wireguard", body),
    update: (id: number, body: { enabled?: boolean; name?: string; countryCode?: string; city?: string }) =>
      patch<WireguardConfig>(`/providers/wireguard/${id}`, body),
    remove: (id: number) => del(`/providers/wireguard/${id}`),
  },
  alerts: {
    channels: () => get<AlertChannel[]>("/alerts/channels"),
    createChannel: (body: AlertChannelInput) => post<AlertChannel>("/alerts/channels", body),
    updateChannel: (id: number, body: Partial<AlertChannelInput>) => patch<AlertChannel>(`/alerts/channels/${id}`, body),
    removeChannel: (id: number) => del(`/alerts/channels/${id}`),
    testChannel: (id: number) => post<void>(`/alerts/channels/${id}/test`),
    rules: () => get<AlertRule[]>("/alerts/rules"),
    createRule: (body: AlertRuleInput) => post<AlertRule>("/alerts/rules", body),
    updateRule: (id: number, body: Partial<AlertRuleInput>) => patch<AlertRule>(`/alerts/rules/${id}`, body),
    removeRule: (id: number) => del(`/alerts/rules/${id}`),
  },
  settings: {
    get: () => get<Settings>("/settings"),
    update: (body: SettingsInput) => put<Settings>("/settings", body),
  },
  admins: {
    list: () => get<Admin[]>("/admins"),
    create: (body: AdminInput) => post<Admin>("/admins", body),
    update: (id: number, body: AdminPatch) => patch<Admin>(`/admins/${id}`, body),
    remove: (id: number) => del(`/admins/${id}`),
  },
  tokens: {
    list: () => get<ApiToken[]>("/tokens"),
    create: (body: { name: string; scopes: TokenScope[]; expiresAt?: string }) => post<CreateTokenResponse>("/tokens", body),
    remove: (id: number) => del(`/tokens/${id}`),
  },
}

