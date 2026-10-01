# lanepool

**A self-hosted rotating proxy over many VPN exits.**

lanepool connects to many WireGuard VPN servers at once (for example dozens of
Surfshark locations, or any provider's WireGuard configs) and puts them behind
one authenticated HTTP/SOCKS5 proxy. Each VPN connection is a *lane* with its own
exit IP. Your apps send requests through the proxy, and lanepool spreads them
across lanes, keeps sessions sticky when asked, avoids lanes a site has blocked,
and records what happened.

```
                              ┌─► lane us-nyc ─► 138.x.x.x
your apps ─► lanepool proxy ──┼─► lane de-fra ─► 185.x.x.x      dashboard: users, live connections,
  (user:pass, :8080)          ├─► lane jp-tok ─► 45.x.x.x       logs, analytics, alerts, settings
                              └─► …
```

## Features

- **Lanes:** WireGuard tunnels run inside the process (wireguard-go with a
  userspace network stack). No root, no `NET_ADMIN`, and no changes to the host's
  routing. DNS is resolved through each lane.
- **Gentle on providers:** lanes connect one at a time, and a failed lane is retried
  later with the next key. Repeated failures pause new connections. Working lanes
  are never restarted on configuration changes. (VPN providers block IPs that
  open many sessions at once.)
- **Proxy users** with:
  - allowed countries and lanes
  - sticky sessions
  - bandwidth quotas and expiry dates
  - concurrent-connection and connections-per-second limits
  - allowed and denied domains
  - client IP allowlists
- **Username parameters** like commercial proxies: `alice-country-us`,
  `alice-session-abc123` (same exit for 10 minutes), `alice-sessttl-30`, `alice-lane-<id>`.
- **Burned-IP avoidance:** mark an exit IP as blocked by a domain, from the
  dashboard or from your app through the API, and lanepool stops using that lane
  for that domain. Repeated connection failures are detected automatically.
- **Live connections:** see who is connected, from where, through which exit IP
  and to which domain, and close any connection.
- **Logs:** one record per connection (user, client IP, target `host:port` when
  enabled per user, lane, exit IP, bytes, duration, result), searchable and
  exportable to CSV. Kept 7 days by default. Also system events and an audit log
  of every admin action.
- **Analytics:** traffic and connections over time, and top users, lanes,
  countries and domains.
- **Alerts** to Telegram, Discord, Slack or any webhook: too few lanes up, a lane
  down, a key failing, a user at their quota, the engine offline, or bursts of
  failed logins.
- **Admin:**
  - multiple admins with `admin` and `viewer` roles
  - scoped API tokens for automation
  - Prometheus `/metrics`
  - restart the proxy, single lanes, or all lanes (paced)
  - maintenance mode

## Architecture

| Component | Role |
|---|---|
| `lanepool control` | Dashboard (React + shadcn/ui, embedded), REST API, Postgres |
| `lanepool engine` | The lanes and the proxy (HTTP CONNECT, plain HTTP and SOCKS5 on one port) |
| Postgres | Users, settings, keys (encrypted), logs, usage, events |

The engine pulls its configuration from control and pushes state, connection
records and usage every two seconds. It never touches the database. Redeploying
the dashboard therefore doesn't drop VPN sessions, and engines could later run on
separate exit servers with their own IPs.

## Quick start (Docker Compose)

```sh
git clone https://github.com/Manan-Santoki/lanepool.git && cd lanepool
cp .env.example .env
# fill in LANEPOOL_SECRET, ENGINE_TOKEN and POSTGRES_PASSWORD, e.g. with openssl rand -hex 32
docker compose up -d
```

1. Open <http://127.0.0.1:8000> and create the first admin.
2. **Providers:**
   - **Surfshark:** add one or more WireGuard private keys (Surfshark → VPN →
     Manual setup → Router → WireGuard → generate a key pair) and choose how many
     locations to use.
   - **Any provider:** paste wg-quick configs under *WireGuard configs*.
3. **Users:** create a proxy user. The dashboard shows ready-to-copy proxy URLs.

```sh
curl -x http://USER:PASS@127.0.0.1:8080 https://api.ipify.org          # rotates
curl -x socks5h://USER-country-de:PASS@127.0.0.1:8080 https://api.ipify.org
curl -x http://USER-session-job42:PASS@127.0.0.1:8080 https://api.ipify.org  # sticky
```

### Running the image directly

The image runs `lanepool all` by default (control and engine in one process).
For two containers, use `lanepool control` and `lanepool engine`, as in
`docker-compose.yml`.

| Variable | Used by | Default |
|---|---|---|
| `DATABASE_URL` | control | `postgres://lanepool:lanepool@localhost:5432/lanepool?sslmode=disable` |
| `LANEPOOL_SECRET` | control | required |
| `ENGINE_TOKEN` | both | required (generated in `all` mode) |
| `ENGINE_URL` | control | `http://localhost:9090` |
| `CONTROL_URL` | engine | `http://localhost:8000` |
| `LISTEN` / `PROXY_LISTEN` / `ENGINE_LISTEN` | | `:8000` / `:8080` / `:9090` |
| `COOKIE_SECURE` | control | `false`; set `true` behind HTTPS |
| `TRUSTED_PROXIES` | engine | CIDRs allowed to send PROXY protocol headers |
| `ADMIN_EMAIL`, `ADMIN_PASSWORD` | control | optional first admin |

Create or reset an admin from the command line:

```sh
docker compose exec control lanepool admin create --email you@example.com
```

## Exposing the proxy

The engine's port 8080 speaks plain HTTP and SOCKS5. To use it from other
machines, put TLS in front: [`deploy/traefik-public-proxy.yml`](deploy/traefik-public-proxy.yml)
turns Traefik into an HTTPS proxy endpoint (`https://USER:PASS@proxy.example.com`)
and passes real client IPs to lanepool with PROXY protocol. Always use strong
proxy passwords; open proxies are found and abused within hours.

### Dokploy

Use [`deploy/docker-compose.dokploy.yml`](deploy/docker-compose.dokploy.yml) as
the compose path:
- **Dashboard:** add a domain for service `control`, port `8000`.
- **Proxy for other Dokploy apps:** `http://USER:PASS@lanepool:8080`.
- **Public HTTPS proxy:** use the Traefik file above.

## For apps

Create an API token in *Settings → API tokens*, then:

```sh
# a session's next connection gets a different exit IP
curl -X POST -H "Authorization: Bearer $TOKEN" -d '{"username":"alice","session":"job42"}' https://lanepool.example.com/api/v1/rotate
# this exit IP is blocked by example.com: avoid it there for an hour
curl -X POST -H "Authorization: Bearer $TOKEN" -d '{"domain":"example.com","exitIp":"185.1.2.3","ttlMinutes":60}' https://lanepool.example.com/api/v1/burn
```

The full API is documented in [`docs/api.md`](docs/api.md).

## Things to know

- **VPN exits are shared and well known.** Sites that block VPN ranges may block
  all of them, and IP rotation doesn't hide cookies or browser fingerprints.
- **Pacing matters.** Providers limit how fast an account or IP may open new
  WireGuard sessions. Opening many at once can get your server's IP refused for
  hours. Keep the defaults (one new lane every 10 s, at most 2 connecting) and
  avoid restarting all lanes repeatedly.
- **WireGuard has no disconnect.** After a restart, old sessions still count on
  the provider's side for a while.
- **Use it responsibly:** respect the terms of the sites you access and of your
  VPN provider.

## Development

Requires Go 1.27+, Node 24+ and Postgres 17.

```sh
createdb lanepool_test
go test -race -p 1 ./...                 # uses TEST_DATABASE_URL or localhost/lanepool_test
cd web && npm install && npm run dev     # dashboard on :5173, proxied to :8000
go run ./cmd/lanepool all                # control + engine
go run ./cmd/fakeprovider                # a local fake VPN server to add as a WireGuard config
```

The engine tests run real WireGuard handshakes against in-process fake providers
(`internal/wg/wgtest`), so no VPN account is needed.

## License

MIT
