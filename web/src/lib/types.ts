// API types for the lanepool control API. Mirrors docs/api.md exactly; keep in sync.

export type Role = "admin" | "viewer";
export interface Admin { id: number; email: string; name: string; role: Role; disabled: boolean; createdAt: string; lastLoginAt?: string }

export type LaneStatus = "queued" | "connecting" | "up" | "down" | "backoff" | "disabled" | "standby";
export interface Lane {
  id: string;              // "surfshark:us-nyc" | "wireguard:12"
  name: string;            // "us-nyc"
  provider: "surfshark" | "wireguard";
  country?: string; countryCode?: string; city?: string; virtual?: boolean;
  enabled: boolean;
  status: LaneStatus;
  keyId?: number; keyLabel?: string;
  exitIp?: string; latencyMs?: number; lastHandshake?: string;
  activeConnections: number; rxBytes: number; txBytes: number;
  restarts: number; lastError?: string; nextRetry?: string;
}

export interface GatewayState { listening: boolean; activeConnections: number; pausedUntil?: string /* circuit breaker */ }

export interface EngineStatus { connected: boolean; nodeId?: string; startedAt?: string; lastReportAt?: string }

export interface Overview {
  lanes: { total: number; byStatus: Partial<Record<LaneStatus, number>> };
  uniqueExitIps: number;
  activeConnections: number;
  gateway: GatewayState;
  maintenance: boolean;             // settings.engine.paused
  engine: EngineStatus;
  traffic24h: { bytesUp: number; bytesDown: number; connections: number; failures: number };
  users: { total: number; enabled: number };
  recentEvents: Event[];
}

export interface LiveConn {
  id: string; userId: number; username: string; clientIp: string; target?: string;
  laneId: string; exitIp?: string; protocol: "http" | "connect" | "socks5";
  startedAt: string; bytesUp: number; bytesDown: number;
}

export interface ProxyUser {
  id: number; username: string; enabled: boolean; note: string;
  expiresAt?: string | null;
  allowedCountries: string[];  // ISO codes, lowercase; empty = all
  allowedLanes: string[];      // lane IDs; empty = all
  allowDomains: string[];      // empty = all
  denyDomains: string[];
  allowedCidrs: string[];      // client IPs; empty = any
  stickyMinutes: number;       // 0 = new lane per connection
  maxConnections: number;      // 0 = unlimited
  connPerSecond: number;       // 0 = unlimited
  quotaBytes: number;          // per period, 0 = unlimited
  usedBytes: number; periodStart: string;
  logDestinations: boolean;    // record target host:port in connection logs
  activeConnections: number;
  createdAt: string; updatedAt: string; lastSeenAt?: string;
}
export type ProxyUserInput = Partial<Omit<ProxyUser, "id" | "usedBytes" | "periodStart" | "activeConnections" | "createdAt" | "updatedAt" | "lastSeenAt">> & { password?: string };

export type ConnResult = "ok" | "auth_failed" | "denied" | "quota" | "rate_limited" | "no_lane" | "dial_failed" | "kicked" | "paused" | "bad_request";
export interface ConnLog {
  id: string; startedAt: string; endedAt: string; durationMs: number;
  userId?: number; username?: string; clientIp: string; target?: string;
  laneId?: string; exitIp?: string; protocol: string;
  bytesUp: number; bytesDown: number; result: ConnResult; error?: string;
}

export interface Event { id: number; time: string; level: "info" | "warn" | "error"; type: string; laneId?: string; actor?: string; message: string }

export interface Page<T> { items: T[]; nextCursor?: string }

export interface BurnedIp { id: number; domain: string; laneId: string; laneName?: string; exitIp?: string; source: "manual" | "auto" | "api"; note: string; createdAt: string; expiresAt: string }

export interface SurfsharkKey { id: number; label: string; publicKey: string; enabled: boolean; createdAt: string; lanes: number; upLanes: number; managed: boolean; expiresAt?: string }
export interface SurfsharkRemoteKey { id: string; name: string; pubKey: string; expiresAt?: string; createdAt?: string; localKeyId?: number }
export interface SurfsharkAccount {
  connected: boolean; email: string; autoManage: boolean; lanesPerKey: number; rotateFailures: number;
  lastError?: string; lastSyncAt?: string | null; remoteKeys?: SurfsharkRemoteKey[]
}
export interface SurfsharkSelection { lanes: number; countries: string[]; excludeCountries: string[]; locations: string[]; excludeLocations: string[]; includeVirtual: boolean; allServers: boolean; spreadKeys: boolean }
export interface SurfsharkProvider { keys: SurfsharkKey[]; selection: SurfsharkSelection; serverCount: number; lastFetchedAt?: string; fetchError?: string }
export interface SurfsharkLocation { id: string; country: string; countryCode: string; city: string; virtual: boolean; load: number }
export interface WireguardConfig { id: number; name: string; countryCode: string; city: string; endpoint: string; enabled: boolean; createdAt: string }

export type ChannelKind = "telegram" | "discord" | "slack" | "webhook";
export interface AlertChannel { id: number; name: string; kind: ChannelKind; enabled: boolean; config: { botToken?: string; chatId?: string; webhookUrl?: string } /* secrets masked as "••••1234" on read */ }
export type AlertEvent = "lanes_below" | "lane_down" | "breaker_open" | "key_failing" | "quota_reached" | "engine_offline" | "auth_failures";
export interface AlertRule { id: number; name: string; event: AlertEvent; threshold: number; cooldownMinutes: number; channelIds: number[]; enabled: boolean; lastFiredAt?: string }

export type Strategy = "round_robin" | "random" | "least_connections" | "lowest_latency";
export interface EngineSettings {
  strategy: Strategy; paused: boolean;
  laneStartDelay: number; maxConnecting: number; connectTimeout: number;
  retryBackoff: number; retryBackoffMax: number; breakerFailures: number; breakerPause: number;
  handshakeMaxAge: number; ipCheckUrl: string; ipCheckInterval: number;
  dialTimeout: number; idleTimeout: number; autoBurnFailures: number; autoBurnTtl: number;  // durations in seconds
}
export interface AppSettings {
  logRetentionDays: number;        // connection logs; default 7
  eventRetentionDays: number;      // default 90
  publicProxyHost: string;         // e.g. proxy.example.com, used to show ready-made proxy URLs
  publicHttpsPort: number;         // 443 when TLS is terminated by Traefik; 0 = none
  publicHttpPort: number;          // plain HTTP/SOCKS port if exposed; 0 = none
  quotaPeriod: "monthly" | "never";
}
export interface Settings { engine: EngineSettings; app: AppSettings }

export interface ApiToken { id: number; name: string; prefix: string; scopes: ("read" | "manage" | "rotate")[]; createdAt: string; lastUsedAt?: string; expiresAt?: string }

export interface TrafficPoint { t: string; bytesUp: number; bytesDown: number; connections: number; failures: number }
export interface TopItem { key: string; label: string; bytes: number; connections: number; failures: number }

/** Alias for `Event` that does not shadow the DOM `Event` global at import sites. */
export type AppEvent = Event;

// Request/response envelopes from the endpoint tables in docs/api.md.
export interface SetupStatus { needsSetup: boolean }
export interface AdminEnvelope { admin: Admin }
/** `partial`: standby lanes are left out; lanes missing from `lanes` are on standby. */
export interface StreamState { lanes: Lane[]; gateway: GatewayState; engine: EngineStatus; activeConnections: number; partial?: boolean }
export interface CreateUserResponse { user: ProxyUser; password?: string }
export interface PasswordResponse { password: string }
export interface KickResponse { kicked: number }
export interface CreateTokenResponse { token: string; apiToken: ApiToken }
export type TokenScope = ApiToken["scopes"][number];
export type AnalyticsRange = "24h" | "7d" | "30d" | "90d";
export type TopBy = "user" | "lane" | "country" | "domain";
export interface BurnInput { domain: string; laneId?: string; exitIp?: string; ttlMinutes: number; note?: string }
export interface WireguardInput { name: string; config: string; countryCode?: string; city?: string }
export interface AdminInput { email: string; name: string; role: Role; password: string }
export interface AdminPatch { name?: string; role?: Role; disabled?: boolean; password?: string }
export interface SettingsInput { engine?: Partial<EngineSettings>; app?: Partial<AppSettings> }
export type AlertChannelInput = Omit<AlertChannel, "id">;
export type AlertRuleInput = Omit<AlertRule, "id" | "lastFiredAt">;
