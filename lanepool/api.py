"""Small status API and dashboard."""

from __future__ import annotations

import base64
import hmac
import json
import logging
import random
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from importlib import resources

from .socks import http_get_via_socks
from .supervisor import Supervisor

log = logging.getLogger("lanepool.api")


def make_server(sup: Supervisor) -> ThreadingHTTPServer:
    settings = sup.settings
    dashboard = resources.files("lanepool").joinpath("dashboard.html").read_bytes()

    class Handler(BaseHTTPRequestHandler):
        server_version = "lanepool"

        def log_message(self, fmt, *args):  # noqa: N802 - keep access logs quiet
            log.debug("%s - %s", self.address_string(), fmt % args)

        def _authorized(self) -> bool:
            if not settings.auth_enabled:
                return True
            header = self.headers.get("Authorization", "")
            if not header.startswith("Basic "):
                return False
            try:
                user, _, password = base64.b64decode(header[6:]).decode().partition(":")
            except (ValueError, UnicodeDecodeError):
                return False
            return hmac.compare_digest(user.encode(), settings.proxy_user.encode()) & hmac.compare_digest(
                password.encode(), settings.proxy_pass.encode()
            )

        def _send(self, status: int, body: bytes, content_type: str = "application/json") -> None:
            self.send_response(status)
            self.send_header("Content-Type", content_type)
            self.send_header("Content-Length", str(len(body)))
            self.send_header("Cache-Control", "no-store")
            if status == 401:
                self.send_header("WWW-Authenticate", 'Basic realm="lanepool"')
            self.end_headers()
            self.wfile.write(body)

        def _json(self, status: int, data) -> None:
            self._send(status, json.dumps(data, indent=2).encode())

        def _lane(self, key: str):
            for lane in sup.lanes:
                if lane.name == key or str(lane.port) == key or str(lane.index) == key:
                    return lane
            return None

        def _summary(self) -> dict:
            with sup.lock:
                lanes = [l.to_dict() for l in sup.lanes]
            counts: dict[str, int] = {}
            for lane in lanes:
                counts[lane["status"]] = counts.get(lane["status"], 0) + 1
            return {
                "healthy": sup.healthy(),
                "uptime_s": int(time.time() - sup.started_at),
                "proxy_port": settings.proxy_port,
                "strategy": settings.strategy,
                "auth": settings.auth_enabled,
                "lanes_total": len(lanes),
                "lanes_by_status": counts,
                "unique_exit_ips": len({l["exit_ip"] for l in lanes if l["exit_ip"]}),
                "new_connections_paused_s": sup.pause_remaining(),
                "lanes": lanes,
            }

        def _rotation_test(self, attempts: int = 6) -> dict:
            """Send a few requests through the rotating proxy and report the exit IPs seen."""
            results = []
            for _ in range(attempts):
                try:
                    body, elapsed = http_get_via_socks(
                        "127.0.0.1", settings.proxy_port, settings.ip_check_url,
                        settings.proxy_user, settings.proxy_pass,
                    )
                    results.append({"exit_ip": body.splitlines()[0] if body else None,
                                    "latency_ms": int(elapsed * 1000)})
                except Exception as exc:  # noqa: BLE001 - report every failure
                    results.append({"error": str(exc) or exc.__class__.__name__})
            ips = [r["exit_ip"] for r in results if r.get("exit_ip")]
            return {"attempts": attempts, "ok": len(ips), "distinct_exit_ips": len(set(ips)), "results": results}

        def do_GET(self):  # noqa: N802
            path = self.path.split("?", 1)[0].rstrip("/") or "/"
            if path == "/healthz":
                ok = sup.healthy()
                return self._json(200 if ok else 503, {"healthy": ok})
            if not self._authorized():
                return self._json(401, {"error": "unauthorized"})
            if path == "/":
                return self._send(200, dashboard, "text/html; charset=utf-8")
            if path == "/api/status":
                return self._json(200, self._summary())
            if path == "/api/lanes":
                with sup.lock:
                    return self._json(200, [l.to_dict() for l in sup.lanes])
            if path == "/api/rotation-test":
                return self._json(200, self._rotation_test())
            if path == "/api/lanes/random":
                with sup.lock:
                    up = [l for l in sup.lanes if l.status in ("up", "running")]
                    if not up:
                        return self._json(503, {"error": "no healthy lanes"})
                    return self._json(200, random.choice(up).to_dict())
            if path.startswith("/api/lanes/"):
                lane = self._lane(path.removeprefix("/api/lanes/"))
                if lane is None:
                    return self._json(404, {"error": "lane not found"})
                with sup.lock:
                    return self._json(200, lane.to_dict(include_output=True))
            return self._json(404, {"error": "not found"})

        def do_POST(self):  # noqa: N802
            if not self._authorized():
                return self._json(401, {"error": "unauthorized"})
            path = self.path.split("?", 1)[0].rstrip("/")
            parts = path.removeprefix("/api/lanes/").split("/")
            if not path.startswith("/api/lanes/") or len(parts) != 2:
                return self._json(404, {"error": "not found"})
            lane = self._lane(parts[0])
            if lane is None:
                return self._json(404, {"error": "lane not found"})
            if parts[1] == "restart":
                sup.restart_lane(lane)
                return self._json(202, {"restarting": lane.name})
            if parts[1] == "check":
                sup.check_now(lane)
                return self._json(202, {"checking": lane.name})
            return self._json(404, {"error": "not found"})

    server = ThreadingHTTPServer(("0.0.0.0", settings.api_port), Handler)
    server.daemon_threads = True
    return server
