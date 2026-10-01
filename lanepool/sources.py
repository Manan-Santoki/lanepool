"""Where lanes come from: Surfshark's public server list and/or WireGuard .conf files."""

from __future__ import annotations

import configparser
import json
import logging
import re
import urllib.request
from collections import defaultdict
from dataclasses import dataclass
from pathlib import Path

from .config import Settings

log = logging.getLogger("lanepool.sources")

_KEY_RE = re.compile(r"^[A-Za-z0-9+/]{42}[AEIMQUYcgkosw480]=$")


@dataclass
class LaneSpec:
    """A WireGuard exit that will become one lane."""

    name: str
    source: str  # "surfshark" or "file"
    wg_config: str  # full wg-quick style config text
    country: str = ""
    country_code: str = ""
    location: str = ""
    endpoint: str = ""
    key_slot: int | None = None  # which SURFSHARK_PRIVATE_KEYS entry (0-based)


def valid_wireguard_key(key: str) -> bool:
    return bool(_KEY_RE.match(key))


# --- Surfshark ---------------------------------------------------------------


def fetch_surfshark_servers(url: str, timeout: float = 20) -> list[dict]:
    req = urllib.request.Request(url, headers={"User-Agent": "lanepool"})
    with urllib.request.urlopen(req, timeout=timeout) as resp:
        data = json.load(resp)
    if not isinstance(data, list):
        raise ValueError("unexpected Surfshark server list format")
    return [s for s in data if s.get("connectionName") and s.get("pubKey")]


def _location_id(server: dict) -> str:
    # "us-nyc.prod.surfshark.com" -> "us-nyc"
    return server["connectionName"].split(".", 1)[0].lower()


def select_surfshark_servers(servers: list[dict], settings: Settings, limit: int) -> list[dict]:
    """Pick up to `limit` servers.

    With SURFSHARK_LOCATIONS the given order is kept. Otherwise locations are spread
    across countries: every country's first location, then every country's second,
    and so on. Ordering is alphabetical (not by load) so port numbers stay stable
    across restarts.
    """
    by_id = {_location_id(s): s for s in servers}

    if settings.surfshark_locations:
        chosen = []
        for wanted in settings.surfshark_locations:
            if wanted in by_id:
                chosen.append(by_id[wanted])
            else:
                log.warning("SURFSHARK_LOCATIONS: %r not found in the server list", wanted)
        return chosen[:limit]

    candidates = []
    for server in servers:
        code = str(server.get("countryCode", "")).lower()
        if settings.countries and code not in settings.countries:
            continue
        if code in settings.exclude_countries:
            continue
        if not settings.include_virtual and "virtual" in server.get("tags", []):
            continue
        candidates.append(server)

    per_country: dict[str, list[dict]] = defaultdict(list)
    for server in sorted(candidates, key=_location_id):
        per_country[str(server.get("countryCode", "")).lower()].append(server)

    chosen = []
    depth = 0
    while len(chosen) < limit and any(len(v) > depth for v in per_country.values()):
        for code in sorted(per_country):
            if depth < len(per_country[code]) and len(chosen) < limit:
                chosen.append(per_country[code][depth])
        depth += 1
    return chosen


def surfshark_wg_config(server: dict, settings: Settings, private_key: str) -> str:
    return (
        "[Interface]\n"
        f"PrivateKey = {private_key}\n"
        f"Address = {settings.surfshark_address}\n"
        f"DNS = {settings.surfshark_dns}\n"
        "\n"
        "[Peer]\n"
        f"PublicKey = {server['pubKey']}\n"
        "AllowedIPs = 0.0.0.0/0\n"
        f"Endpoint = {server['connectionName']}:51820\n"
        "PersistentKeepalive = 25\n"
    )


def surfshark_lanes(settings: Settings, limit: int) -> list[LaneSpec]:
    servers = fetch_surfshark_servers(settings.surfshark_api)
    log.info("Surfshark server list: %d locations", len(servers))
    keys = settings.surfshark_private_keys
    lanes = []
    # Surfshark limits how many servers one key can be connected to at once, so
    # lanes are spread round-robin across all configured keys.
    for i, server in enumerate(select_surfshark_servers(servers, settings, limit)):
        slot = i % len(keys)
        lanes.append(
            LaneSpec(
                name=_location_id(server),
                source="surfshark",
                wg_config=surfshark_wg_config(server, settings, keys[slot]),
                country=server.get("country", ""),
                country_code=str(server.get("countryCode", "")).upper(),
                location=server.get("location", ""),
                endpoint=f"{server['connectionName']}:51820",
                key_slot=slot,
            )
        )
    return lanes


# --- WireGuard files ---------------------------------------------------------


def _clean_wg_config(text: str) -> tuple[str, str]:
    """Normalise a wg-quick config for wireproxy. Returns (config, endpoint).

    wireproxy only accepts /32 (IPv4) and /128 (IPv6) interface addresses and
    ignores wg-quick-only keys, so those are dropped or rewritten here.
    """
    parser = configparser.ConfigParser(strict=False, interpolation=None)
    parser.optionxform = str  # keep key case
    parser.read_string(text)

    if not parser.has_section("Interface") or not parser.has_section("Peer"):
        raise ValueError("missing [Interface] or [Peer] section")

    interface = parser["Interface"]
    if "PrivateKey" not in interface:
        raise ValueError("missing PrivateKey")

    addresses = []
    for addr in interface.get("Address", "").split(","):
        addr = addr.strip()
        if not addr:
            continue
        ip = addr.split("/", 1)[0]
        addresses.append(f"{ip}/128" if ":" in ip else f"{ip}/32")
    if not addresses:
        raise ValueError("missing Address")

    lines = ["[Interface]", f"PrivateKey = {interface['PrivateKey']}", f"Address = {', '.join(addresses)}"]
    for key in ("DNS", "MTU"):
        if key in interface:
            lines.append(f"{key} = {interface[key]}")

    peer = parser["Peer"]
    for key in ("PublicKey", "Endpoint"):
        if key not in peer:
            raise ValueError(f"missing {key} in [Peer]")
    lines += ["", "[Peer]"]
    for key in ("PublicKey", "PresharedKey", "AllowedIPs", "Endpoint", "PersistentKeepalive"):
        if key in peer:
            lines.append(f"{key} = {peer[key]}")
    if "AllowedIPs" not in peer:
        lines.append("AllowedIPs = 0.0.0.0/0")
    return "\n".join(lines) + "\n", peer["Endpoint"]


def file_lanes(config_dir: Path, limit: int) -> list[LaneSpec]:
    if not config_dir.is_dir() or limit <= 0:
        return []
    lanes = []
    for path in sorted(config_dir.glob("*.conf")):
        if len(lanes) >= limit:
            break
        try:
            config, endpoint = _clean_wg_config(path.read_text())
        except (configparser.Error, ValueError, OSError) as exc:
            log.error("skipping %s: %s", path.name, exc)
            continue
        name = re.sub(r"[^a-z0-9-]+", "-", path.stem.lower()).strip("-") or "lane"
        lanes.append(LaneSpec(name=name, source="file", wg_config=config, endpoint=endpoint))
    return lanes


def collect_lanes(settings: Settings) -> list[LaneSpec]:
    """Files in CONFIG_DIR come first, then Surfshark fills the remaining lanes."""
    lanes = file_lanes(settings.config_dir, settings.lanes)
    if lanes:
        log.info("loaded %d lane(s) from %s", len(lanes), settings.config_dir)

    remaining = settings.lanes - len(lanes)
    keys = settings.surfshark_private_keys
    if keys and remaining > 0:
        for n, key in enumerate(keys, 1):
            if not valid_wireguard_key(key):
                raise ValueError(f"Surfshark key #{n} is not a valid WireGuard private key")
        surfshark = surfshark_lanes(settings, remaining)
        log.info("%d Surfshark lane(s) across %d key(s), about %d per key",
                 len(surfshark), len(keys), -(-len(surfshark) // len(keys)))
        lanes += surfshark

    # Names are used in URLs and file names, so make them unique.
    seen: dict[str, int] = {}
    for lane in lanes:
        count = seen.get(lane.name, 0)
        seen[lane.name] = count + 1
        if count:
            lane.name = f"{lane.name}-{count + 1}"
    return lanes
