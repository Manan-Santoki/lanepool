# lanepool

**Many VPN exits, one rotating proxy.**

lanepool connects to many VPN servers at the same time (for example 99 Surfshark
locations) and exposes them as proxies:

- **one rotating proxy** (`:8080`, HTTP and SOCKS5) that sends each new connection
  through a different exit IP, and
- **one SOCKS5 port per lane** (`:10001` … `:10099`) when you want to pin requests to
  a single exit IP.

```
                                  ┌─► lane :10001 ─► Surfshark al-tia  ─► 31.171.x.x
your app ─► rotating proxy :8080 ─┼─► lane :10002 ─► Surfshark de-fra  ─► 185.x.x.x
            (round robin,         ├─► lane :10003 ─► Surfshark us-nyc  ─► 138.x.x.x
             health checked)      └─► … ×99
```

Everything runs in **one unprivileged container**: no `NET_ADMIN`, no kernel WireGuard,
and no changes to the host's routing. Each lane is a userspace WireGuard client.

## How it works

lanepool reuses two existing open-source projects unmodified and adds the glue:

| Piece | Project | Role |
|---|---|---|
| Lanes | [wireproxy](https://github.com/pufferffish/wireproxy) | One process per VPN server; turns a WireGuard config into a local SOCKS5 proxy in userspace |
| Rotation | [glider](https://github.com/nadoo/glider) | Front proxy that spreads connections across the lanes and skips unhealthy ones |
| Glue | lanepool (this repo) | Builds the Surfshark configs, supervises and restarts every process, looks up each lane's exit IP, serves the dashboard and API |

The official release binaries of both are downloaded at image build time and
verified against pinned SHA-256 checksums (see the `Dockerfile`).

## Quick start (Docker Compose)

1. **Get a Surfshark WireGuard key.** In the Surfshark web app: *VPN → Manual setup →
   Router (or Desktop) → WireGuard → I don't have a key pair → Generate*. Copy the
   **private** key. Surfshark allows unlimited devices, but **one key can only stay
   connected to about 20–24 servers at once**. For more lanes, generate one key pair
   per 20 lanes and list the extras in `SURFSHARK_PRIVATE_KEYS`, for example 5 keys for 99 lanes.

2. **Configure and start:**

   ```sh
   git clone https://github.com/Manan-Santoki/lanepool.git
   cd lanepool
   cp .env.example .env
   # edit .env and set SURFSHARK_PRIVATE_KEY=...
   docker compose up -d
   ```

3. **Use it:**

   ```sh
   # rotating: every request can leave from a different IP
   curl -x http://127.0.0.1:8080 https://api.ipify.org
   curl -x socks5h://127.0.0.1:8080 https://api.ipify.org

   # pinned: always lane 42
   curl -x socks5h://127.0.0.1:10042 https://api.ipify.org
   ```

   Open the dashboard at <http://127.0.0.1:8000>.

Lanes take a few seconds to connect. The dashboard shows each lane's port, location,
exit IP and latency.

## Configuration

All settings are environment variables in `.env`. See [`.env.example`](.env.example)
for the full, commented list. The most useful ones:

| Variable | Default | Meaning |
|---|---|---|
| `SURFSHARK_PRIVATE_KEY` | – | WireGuard private key from the Surfshark dashboard |
| `SURFSHARK_PRIVATE_KEYS` | – | More keys, comma separated; lanes are spread evenly across all keys |
| `LANES` | `99` | Number of lanes (exit IPs) |
| `COUNTRIES` / `EXCLUDE_COUNTRIES` | – | Comma-separated country codes to include or skip |
| `SURFSHARK_LOCATIONS` | – | Exact locations in order, e.g. `us-nyc,de-fra,uk-lon` |
| `INCLUDE_VIRTUAL` | `true` | Include Surfshark "virtual" locations |
| `BIND_IP` | `127.0.0.1` | Host IP the ports are published on |
| `PROXY_PORT` | `8080` | Rotating proxy port (HTTP + SOCKS5) |
| `PROXY_USER` / `PROXY_PASS` | – | Auth for the rotating proxy, every lane and the dashboard |
| `STRATEGY` | `rr` | `rr` round robin, `dh` same host → same lane, `ha` failover, `lha` lowest latency |
| `IP_CHECK_INTERVAL` | `600` | Seconds between exit-IP lookups per lane (`0` disables) |

### Choosing lanes

Without filters, lanes are spread across countries first: one location in each of
about 100 countries, then a second location per country, and so on. The order is
alphabetical rather than by server load, so **a lane keeps its port across restarts**
as long as Surfshark's location list doesn't change.

### Other providers

Any WireGuard config works. Put `.conf` files in `./configs/` (mounted read-only at
`/config/wireguard`). Files become the first lanes in alphabetical order, and
Surfshark fills the rest up to `LANES`. Leave `SURFSHARK_PRIVATE_KEY` empty to use only
files. wg-quick-only keys such as `PostUp` are ignored, and addresses are narrowed to
`/32` and `/128` as wireproxy requires.

### Changing the number of lanes

The compose file publishes ports `10001-10099` for 99 lanes. If you change `LANES`,
change that range to `10001-<10000 + LANES>`.

## API

| Method | Path | Returns |
|---|---|---|
| GET | `/healthz` | `200` when the rotating proxy runs and at least one lane is up (no auth) |
| GET | `/api/status` | Summary plus every lane |
| GET | `/api/lanes` | All lanes |
| GET | `/api/rotation-test` | Sends 6 requests through the rotating proxy and lists the exit IPs seen |
| GET | `/api/lanes/random` | A random healthy lane (pick its `port` to pin a request) |
| GET | `/api/lanes/{name\|port\|index}` | One lane, including its recent wireproxy output |
| POST | `/api/lanes/{lane}/restart` | Reconnect a lane |
| POST | `/api/lanes/{lane}/check` | Re-check a lane's exit IP now |

When `PROXY_USER` and `PROXY_PASS` are set, everything except `/healthz` requires HTTP
basic auth with the same credentials.

### Example: Python

```python
import requests

PROXY = "http://user:pass@127.0.0.1:8080"
for _ in range(5):
    r = requests.get("https://api.ipify.org", proxies={"http": PROXY, "https": PROXY})
    print(r.text)  # a different exit IP each time with STRATEGY=rr
```

Round robin rotates per **connection**. HTTP clients that reuse connections, such as
`requests.Session`, keep the same lane until the connection closes. To rotate per
request, use a fresh connection each time or pin a lane port explicitly.

## Deploying on Dokploy

Use `docker-compose.dokploy.yml`. It builds from this repo, joins Dokploy's shared
`dokploy-network` and publishes nothing publicly.

1. Create a Compose service from this GitHub repo with compose path
   `./docker-compose.dokploy.yml`.
2. In *Environment*, set at least `SURFSHARK_PRIVATE_KEY`, `PROXY_USER` and `PROXY_PASS`.
3. Deploy. Other Dokploy apps then use `http://USER:PASS@lanepool:8080`.

To see the dashboard, open an SSH tunnel with `ssh -L 8000:127.0.0.1:8000 your-server`
and browse to <http://127.0.0.1:8000>.

## Exposing it to other machines

By default every port binds to `127.0.0.1` on the host. To let another server use the
pool, set `BIND_IP=0.0.0.0`, **set `PROXY_USER` and `PROXY_PASS`**, and firewall the
ports so only your application servers can reach them. Otherwise anyone who finds the
ports can use your VPN account. Don't put the dashboard on the public internet
without auth.

## Resources

Each lane is a separate wireproxy process using roughly 15–40 MB of RAM, so 99 lanes
need about 2–4 GB. Start with fewer lanes (`LANES=10`) on small machines.

## Limits and responsible use

- **VPN IPs are well known.** Many sites rate-limit or block commercial VPN ranges as a
  whole, and the IPs are shared with other VPN users. Rotating across 99 VPN exits
  doesn't guarantee being treated as 99 separate clients.
- **IP is only one signal.** Cookies, TLS and browser fingerprints, and request
  patterns can still link your traffic.
- **Follow the rules you agreed to.** Respect the terms of the sites you access and of
  your VPN provider, honour `robots.txt` and rate limits where they apply, and prefer
  an official API when one exists.

## Development

```sh
python3 -m unittest discover -s tests -v
```

The tests use fake `wireproxy` and `glider` binaries (`tests/fakes/`), so they run
without Docker or a VPN account. CI runs them and builds a multi-arch image
(`linux/amd64`, `linux/arm64`), which is published to
`ghcr.io/manan-santoki/lanepool`.

## License

MIT. wireproxy (ISC) and glider (GPL-3.0) are separate programs bundled unmodified in
the container image, under their own licenses.
