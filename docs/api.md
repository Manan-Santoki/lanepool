# lanepool control API

All endpoints are JSON under `/api`. The dashboard authenticates with an HttpOnly
session cookie (`lp_session`, set by login/setup). Apps use `Authorization: Bearer <token>`
with an API token.

- **Roles:** `viewer` may only use GET. `admin` may do everything.
- **API token scopes:** `read` (GET), `manage` (everything an admin can do), `rotate`
  (`POST /api/v1/rotate`, `POST /api/v1/burn`, `GET /api/lanes/random`).
- **Errors:** non-2xx with `{ "error": string, "fields"?: Record<string,string> }`.
  `401` means not logged in, `403` means not allowed.
- **Times** are ISO 8601 strings. **Bytes** are numbers.
- **Mutations** are recorded in the audit log (events with `type` starting with `admin.`).

## Types (TypeScript)

```ts
type Role = "admin" | "viewer";
interface Admin { id: number; email: string; name: string; role: Role; disabled: boolean; createdAt: string; lastLoginAt?: string }

type LaneStatus = "queued" | "connecting" | "up" | "down" | "backoff" | "disabled";
interface Lane {
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

interface GatewayState { listening: boolean; activeConnections: number; pausedUntil?: string /* circuit breaker */ }

interface EngineStatus { connected: boolean; nodeId?: string; startedAt?: string; lastReportAt?: string }

interface Overview {
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

interface LiveConn {
  id: string; userId: number; username: string; clientIp: string; target?: string;
  laneId: string; exitIp?: string; protocol: "http" | "connect" | "socks5";
  startedAt: string; bytesUp: number; bytesDown: number;
}

interface ProxyUser {
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
type ProxyUserInput = Partial<Omit<ProxyUser, "id" | "usedBytes" | "periodStart" | "activeConnections" | "createdAt" | "updatedAt" | "lastSeenAt">> & { password?: string };

type ConnResult = "ok" | "auth_failed" | "denied" | "quota" | "rate_limited" | "no_lane" | "dial_failed" | "kicked" | "paused" | "bad_request";
interface ConnLog {
  id: string; startedAt: string; endedAt: string; durationMs: number;
  userId?: number; username?: string; clientIp: string; target?: string;
  laneId?: string; exitIp?: string; protocol: string;
  bytesUp: number; bytesDown: number; result: ConnResult; error?: string;
}

interface Event { id: number; time: string; level: "info" | "warn" | "error"; type: string; laneId?: string; actor?: string; message: string }

interface Page<T> { items: T[]; nextCursor?: string }

interface BurnedIp { id: number; domain: string; laneId: string; laneName?: string; exitIp?: string; source: "manual" | "auto" | "api"; note: string; createdAt: string; expiresAt: string }

interface SurfsharkKey { id: number; label: string; publicKey: string; enabled: boolean; createdAt: string; lanes: number; upLanes: number }
interface SurfsharkSelection { lanes: number; countries: string[]; excludeCountries: string[]; locations: string[]; includeVirtual: boolean }
interface SurfsharkProvider { keys: SurfsharkKey[]; selection: SurfsharkSelection; serverCount: number; lastFetchedAt?: string; fetchError?: string }
interface SurfsharkLocation { id: string; country: string; countryCode: string; city: string; virtual: boolean; load: number }
interface WireguardConfig { id: number; name: string; countryCode: string; city: string; endpoint: string; enabled: boolean; createdAt: string }

type ChannelKind = "telegram" | "discord" | "slack" | "webhook";
interface AlertChannel { id: number; name: string; kind: ChannelKind; enabled: boolean; config: { botToken?: string; chatId?: string; webhookUrl?: string } /* secrets masked as "••••1234" on read */ }
type AlertEvent = "lanes_below" | "lane_down" | "breaker_open" | "key_failing" | "quota_reached" | "engine_offline" | "auth_failures";
interface AlertRule { id: number; name: string; event: AlertEvent; threshold: number; cooldownMinutes: number; channelIds: number[]; enabled: boolean; lastFiredAt?: string }

type Strategy = "round_robin" | "random" | "least_connections" | "lowest_latency";
interface EngineSettings {
  strategy: Strategy; paused: boolean;
  laneStartDelay: number; maxConnecting: number; connectTimeout: number;
  retryBackoff: number; retryBackoffMax: number; breakerFailures: number; breakerPause: number;
  handshakeMaxAge: number; ipCheckUrl: string; ipCheckInterval: number;
  dialTimeout: number; idleTimeout: number; autoBurnFailures: number; autoBurnTtl: number;  // durations in seconds
}
interface AppSettings {
  logRetentionDays: number;        // connection logs; default 7
  eventRetentionDays: number;      // default 90
  publicProxyHost: string;         // e.g. proxy.example.com, used to show ready-made proxy URLs
  publicHttpsPort: number;         // 443 when TLS is terminated by Traefik; 0 = none
  publicHttpPort: number;          // plain HTTP/SOCKS port if exposed; 0 = none
  quotaPeriod: "monthly" | "never";
}
interface Settings { engine: EngineSettings; app: AppSettings }

interface ApiToken { id: number; name: string; prefix: string; scopes: ("read" | "manage" | "rotate")[]; createdAt: string; lastUsedAt?: string; expiresAt?: string }

interface TrafficPoint { t: string; bytesUp: number; bytesDown: number; connections: number; failures: number }
interface TopItem { key: string; label: string; bytes: number; connections: number; failures: number }
```

## Endpoints

### Setup and auth
| Method | Path | Body | Returns |
|---|---|---|---|
| GET | /api/setup | | `{ needsSetup: boolean }` (true when no admin exists) |
| POST | /api/setup | `{ email, name, password }` | `{ admin }`; only works while no admin exists; logs in |
| POST | /api/auth/login | `{ email, password }` | `{ admin }` + cookie |
| POST | /api/auth/logout | | `204` |
| GET | /api/auth/me | | `{ admin }` |
| POST | /api/auth/password | `{ currentPassword, newPassword }` | `204` |

### Overview and live data
| Method | Path | Returns |
|---|---|---|
| GET | /api/overview | `Overview` |
| GET | /api/stream | Server-Sent Events. `state`: `{ lanes: Lane[], gateway: GatewayState, engine: EngineStatus, activeConnections: number }` about every 2 s. `event`: `Event` |

### Lanes and gateway
| Method | Path | Body | Returns |
|---|---|---|---|
| GET | /api/lanes | | `Lane[]` |
| PATCH | /api/lanes/:id | `{ enabled }` | `Lane` |
| POST | /api/lanes/:id/restart | | `202` |
| POST | /api/lanes/restart-all | | `202` (lanes reconnect one at a time) |
| GET | /api/lanes/random?country=us | | `Lane` (a random healthy lane) |
| POST | /api/gateway/restart | | `202` (closes open proxy connections; lanes keep running) |
| POST | /api/gateway/maintenance | `{ paused: boolean }` | `204` |

`:id` is URL-encoded (`surfshark%3Aus-nyc`).

### Connections
| Method | Path | Body | Returns |
|---|---|---|---|
| GET | /api/connections | | `LiveConn[]` |
| POST | /api/connections/kick | `{ ids?: string[], userId?: number }` | `{ kicked: number }` |

### Proxy users
| Method | Path | Body | Returns |
|---|---|---|---|
| GET | /api/users | | `ProxyUser[]` |
| POST | /api/users | `ProxyUserInput` (`username` required; `password` optional) | `{ user: ProxyUser, password?: string }`; a password is generated if omitted |
| GET | /api/users/:id | | `ProxyUser` |
| PATCH | /api/users/:id | `ProxyUserInput` | `ProxyUser` |
| POST | /api/users/:id/password | `{ password? }` | `{ password: string }` |
| POST | /api/users/:id/reset-usage | | `ProxyUser` |
| DELETE | /api/users/:id | | `204` |

### Logs and events
| Method | Path | Query | Returns |
|---|---|---|---|
| GET | /api/logs/connections | `from, to, user, lane, result, client, q (target contains), limit (≤500), cursor` | `Page<ConnLog>` newest first |
| GET | /api/logs/connections.csv | same filters | CSV download |
| GET | /api/events | `from, to, level, type (prefix), lane, q, limit, cursor` | `Page<Event>` |

### Analytics
| Method | Path | Query | Returns |
|---|---|---|---|
| GET | /api/analytics/traffic | `range=24h\|7d\|30d\|90d, user?, lane?` | `TrafficPoint[]` (hourly for 24h and 7d, daily otherwise) |
| GET | /api/analytics/top | `range, by=user\|lane\|country\|domain, limit?` | `TopItem[]` (`domain` only covers logged destinations within log retention) |

### Burned IPs
| Method | Path | Body | Returns |
|---|---|---|---|
| GET | /api/burned | | `BurnedIp[]` (active and recently expired) |
| POST | /api/burned | `{ domain, laneId?, exitIp?, ttlMinutes, note? }` | `BurnedIp` |
| DELETE | /api/burned/:id | | `204` |

### Providers
| Method | Path | Body | Returns |
|---|---|---|---|
| GET | /api/providers/surfshark | | `SurfsharkProvider` |
| POST | /api/providers/surfshark/keys | `{ privateKey, label? }` | `SurfsharkKey` |
| PATCH | /api/providers/surfshark/keys/:id | `{ enabled?, label? }` | `SurfsharkKey` |
| DELETE | /api/providers/surfshark/keys/:id | | `204` |
| PUT | /api/providers/surfshark/selection | `SurfsharkSelection` | `SurfsharkProvider` |
| GET | /api/providers/surfshark/locations | | `SurfsharkLocation[]` |
| GET | /api/providers/wireguard | | `WireguardConfig[]` |
| POST | /api/providers/wireguard | `{ name, config /* wg-quick text */, countryCode?, city? }` | `WireguardConfig` |
| PATCH | /api/providers/wireguard/:id | `{ enabled?, name?, countryCode?, city? }` | `WireguardConfig` |
| DELETE | /api/providers/wireguard/:id | | `204` |

### Alerts
| Method | Path | Body | Returns |
|---|---|---|---|
| GET/POST | /api/alerts/channels | `Omit<AlertChannel,"id">` | `AlertChannel[]` / `AlertChannel` |
| PATCH/DELETE | /api/alerts/channels/:id | partial | `AlertChannel` / `204` |
| POST | /api/alerts/channels/:id/test | | `204` (sends a test message) |
| GET/POST | /api/alerts/rules | `Omit<AlertRule,"id"\|"lastFiredAt">` | `AlertRule[]` / `AlertRule` |
| PATCH/DELETE | /api/alerts/rules/:id | partial | `AlertRule` / `204` |

### Settings, admins, tokens
| Method | Path | Body | Returns |
|---|---|---|---|
| GET | /api/settings | | `Settings` |
| PUT | /api/settings | `{ engine?: Partial<EngineSettings>, app?: Partial<AppSettings> }` | `Settings` |
| GET/POST | /api/admins | `{ email, name, role, password }` | `Admin[]` / `Admin` |
| PATCH/DELETE | /api/admins/:id | `{ name?, role?, disabled?, password? }` | `Admin` / `204` (you can't delete or demote yourself) |
| GET/POST | /api/tokens | `{ name, scopes, expiresAt? }` | `ApiToken[]` / `{ token: string /* shown once */, apiToken: ApiToken }` |
| DELETE | /api/tokens/:id | | `204` |

### For apps (API token)
| Method | Path | Body | Returns |
|---|---|---|---|
| POST | /api/v1/rotate | `{ username, session }` | `204`: the session's next connection gets a different lane |
| POST | /api/v1/burn | `{ domain, exitIp?, laneId?, ttlMinutes? }` | `BurnedIp` |
| GET | /metrics | | Prometheus metrics (API token with `read`) |

### Health (no auth)
`GET /healthz` returns liveness. `GET /readyz` returns `200` when the database is
reachable and an engine reported in the last 30 s.

## Proxy usage (shown in the dashboard)

Proxy username parameters (combinable): `USER-country-us`, `USER-session-<id>` (sticky
lane, default 10 min), `USER-sessttl-<minutes>`, `USER-lane-<laneId>`.

Example proxy URLs, from `AppSettings`:
- HTTPS proxy via Traefik: `https://USER:PASS@<publicProxyHost>:<publicHttpsPort>`
- HTTP: `http://USER:PASS@<publicProxyHost>:<publicHttpPort>`
- SOCKS5: `socks5h://USER:PASS@<publicProxyHost>:<publicHttpPort>`
