"""Runs one wireproxy per lane plus the glider rotating proxy, and keeps them alive."""

from __future__ import annotations

import logging
import os
import shutil
import subprocess
import sys
import threading
import time
import urllib.parse
from collections import deque
from concurrent.futures import ThreadPoolExecutor
from dataclasses import dataclass, field
from pathlib import Path

from .config import Settings
from .socks import http_get_via_socks
from .sources import LaneSpec

log = logging.getLogger("lanepool")

WIREPROXY_BIN = os.environ.get("WIREPROXY_BIN", "wireproxy")
GLIDER_BIN = os.environ.get("GLIDER_BIN", "glider")
MAX_BACKOFF = 60.0


@dataclass
class Lane:
    index: int
    port: int
    spec: LaneSpec
    proc: subprocess.Popen | None = None
    status: str = "starting"
    restarts: int = 0
    started_at: float = 0.0
    exit_ip: str = ""
    latency_ms: int | None = None
    last_check: float = 0.0
    last_error: str = ""
    next_check: float = 0.0
    next_start: float = 0.0
    backoff: float = 1.0
    checking: bool = False
    output: deque = field(default_factory=lambda: deque(maxlen=30))

    @property
    def name(self) -> str:
        return self.spec.name

    def to_dict(self, include_output: bool = False) -> dict:
        data = {
            "index": self.index,
            "name": self.name,
            "port": self.port,
            "status": self.status,
            "exit_ip": self.exit_ip or None,
            "latency_ms": self.latency_ms,
            "country": self.spec.country or None,
            "country_code": self.spec.country_code or None,
            "location": self.spec.location or None,
            "endpoint": self.spec.endpoint or None,
            "source": self.spec.source,
            "pid": self.proc.pid if self.proc and self.proc.poll() is None else None,
            "restarts": self.restarts,
            "last_check": self.last_check or None,
            "last_error": self.last_error or None,
        }
        if include_output:
            data["output"] = list(self.output)
        return data


def wireproxy_config(lane_wg_path: Path, bind: str, port: int, settings: Settings) -> str:
    lines = [f"WGConfig = {lane_wg_path}", "", "[Socks5]", f"BindAddress = {bind}:{port}"]
    if settings.auth_enabled:
        lines += [f"Username = {settings.proxy_user}", f"Password = {settings.proxy_pass}"]
    return "\n".join(lines) + "\n"


def glider_config(lanes: list[Lane], settings: Settings) -> str:
    def creds() -> str:
        if not settings.auth_enabled:
            return ""
        q = urllib.parse.quote
        return f"{q(settings.proxy_user, safe='')}:{q(settings.proxy_pass, safe='')}@"

    lines = [
        "verbose=False",
        f"listen=mixed://{creds()}:{settings.proxy_port}",
        f"strategy={settings.strategy}",
        f"check={settings.check_url}",
        f"checkinterval={settings.check_interval}",
        f"maxfailures={settings.max_failures}",
    ]
    for lane in lanes:
        lines.append(f"forward=socks5://{creds()}127.0.0.1:{lane.port}")
    return "\n".join(lines) + "\n"


def _write_private(path: Path, text: str) -> None:
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    with os.fdopen(fd, "w") as fh:
        fh.write(text)


class Supervisor:
    def __init__(self, settings: Settings, specs: list[LaneSpec]):
        self.settings = settings
        self.lock = threading.RLock()
        self.stopping = threading.Event()
        self.started_at = time.time()
        self.lanes = [
            Lane(index=i, port=settings.lane_port_start + i, spec=spec) for i, spec in enumerate(specs)
        ]
        self.glider: subprocess.Popen | None = None
        self.glider_backoff = 1.0
        self.glider_next_start = 0.0
        self.check_host = "127.0.0.1" if settings.lane_bind in ("", "0.0.0.0", "::") else settings.lane_bind
        self.checks = ThreadPoolExecutor(max_workers=16, thread_name_prefix="ipcheck")

    # --- setup -------------------------------------------------------------

    def prepare(self) -> None:
        run = self.settings.run_dir
        # RUN_DIR is usually a tmpfs mount point, so empty it rather than removing it.
        shutil.rmtree(run / "lanes", ignore_errors=True)
        (run / "lanes").mkdir(parents=True, mode=0o700)
        for lane in self.lanes:
            wg_path = run / "lanes" / f"{lane.index:03d}-{lane.name}.wg.conf"
            _write_private(wg_path, lane.spec.wg_config)
            _write_private(
                wg_path.with_name(f"{lane.index:03d}-{lane.name}.wireproxy.conf"),
                wireproxy_config(wg_path, self.settings.lane_bind, lane.port, self.settings),
            )
        _write_private(run / "glider.conf", glider_config(self.lanes, self.settings))

    def _wireproxy_conf(self, lane: Lane) -> Path:
        return self.settings.run_dir / "lanes" / f"{lane.index:03d}-{lane.name}.wireproxy.conf"

    # --- processes ---------------------------------------------------------

    def _pump(self, proc: subprocess.Popen, prefix: str, buffer: deque | None, echo: bool) -> None:
        assert proc.stdout is not None
        for raw in proc.stdout:
            line = raw.decode(errors="replace").rstrip()
            if buffer is not None:
                buffer.append(line)
            if echo:
                print(f"[{prefix}] {line}", file=sys.stdout, flush=True)
        proc.stdout.close()

    def _start_lane(self, lane: Lane) -> None:
        try:
            proc = subprocess.Popen(
                [WIREPROXY_BIN, "-c", str(self._wireproxy_conf(lane))],
                stdout=subprocess.PIPE,
                stderr=subprocess.STDOUT,
                stdin=subprocess.DEVNULL,
            )
        except OSError as exc:
            lane.status = "restarting"
            lane.last_error = f"failed to start wireproxy: {exc}"
            lane.next_start = time.monotonic() + lane.backoff
            lane.backoff = min(lane.backoff * 2, MAX_BACKOFF)
            return
        lane.proc = proc
        lane.started_at = time.monotonic()
        lane.status = "starting" if self.settings.ip_check_interval else "running"
        lane.next_check = time.monotonic() + 5
        threading.Thread(
            target=self._pump,
            args=(proc, lane.name, lane.output, self.settings.lane_logs),
            daemon=True,
        ).start()

    def _start_glider(self) -> None:
        try:
            self.glider = subprocess.Popen(
                [GLIDER_BIN, "-config", str(self.settings.run_dir / "glider.conf")],
                stdout=subprocess.PIPE,
                stderr=subprocess.STDOUT,
                stdin=subprocess.DEVNULL,
            )
        except OSError as exc:
            log.error("failed to start glider: %s", exc)
            self.glider = None
            self.glider_next_start = time.monotonic() + self.glider_backoff
            self.glider_backoff = min(self.glider_backoff * 2, MAX_BACKOFF)
            return
        threading.Thread(target=self._pump, args=(self.glider, "glider", None, True), daemon=True).start()
        log.info("rotating proxy listening on :%d (strategy=%s)", self.settings.proxy_port, self.settings.strategy)

    def restart_lane(self, lane: Lane) -> None:
        with self.lock:
            lane.backoff = 1.0
            lane.next_start = 0.0
            if lane.proc and lane.proc.poll() is None:
                lane.proc.terminate()
            else:
                self._start_lane(lane)

    def _tick(self) -> None:
        now = time.monotonic()
        with self.lock:
            if self.stopping.is_set():
                return
            for lane in self.lanes:
                if lane.proc is not None and lane.proc.poll() is not None:
                    code = lane.proc.returncode
                    tail = lane.output[-1] if lane.output else ""
                    lane.proc = None
                    lane.restarts += 1
                    lane.status = "restarting"
                    lane.last_error = f"wireproxy exited with {code}" + (f": {tail}" if tail else "")
                    # A lane that ran for a while gets a fresh backoff.
                    if now - lane.started_at > 120:
                        lane.backoff = 1.0
                    lane.next_start = now + lane.backoff
                    lane.backoff = min(lane.backoff * 2, MAX_BACKOFF)
                    log.warning("lane %s: %s", lane.name, lane.last_error)
                if lane.proc is None and now >= lane.next_start:
                    self._start_lane(lane)

            if self.glider is not None and self.glider.poll() is not None:
                log.error("glider exited with %s, restarting", self.glider.returncode)
                self.glider = None
                self.glider_next_start = now + self.glider_backoff
                self.glider_backoff = min(self.glider_backoff * 2, MAX_BACKOFF)
            if self.glider is None and now >= self.glider_next_start:
                self._start_glider()

    # --- exit IP checks ----------------------------------------------------

    def _check(self, lane: Lane) -> None:
        s = self.settings
        try:
            body, elapsed = http_get_via_socks(
                self.check_host, lane.port, s.ip_check_url, s.proxy_user, s.proxy_pass
            )
            ip = body.splitlines()[0].strip() if body else ""
            if not ip or len(ip) > 64:
                raise ValueError(f"unexpected IP check response: {body[:80]!r}")
            with self.lock:
                if ip != lane.exit_ip:
                    log.info("lane %s (port %d) exit IP %s", lane.name, lane.port, ip)
                lane.exit_ip, lane.latency_ms, lane.last_error = ip, int(elapsed * 1000), ""
                if lane.proc is not None:
                    lane.status = "up"
        except Exception as exc:  # noqa: BLE001 - any failure marks the lane down
            with self.lock:
                lane.last_error = str(exc) or exc.__class__.__name__
                if lane.proc is not None:
                    lane.status = "down"
        finally:
            with self.lock:
                lane.last_check = time.time()
                # Retry failing lanes sooner than healthy ones.
                interval = s.ip_check_interval if lane.status == "up" else min(60, s.ip_check_interval)
                lane.next_check = time.monotonic() + interval
                lane.checking = False

    def check_now(self, lane: Lane) -> None:
        with self.lock:
            lane.next_check = 0.0

    def _schedule_checks(self) -> None:
        if not self.settings.ip_check_interval:
            return
        now = time.monotonic()
        with self.lock:
            due = [l for l in self.lanes if l.proc is not None and not l.checking and now >= l.next_check]
            for lane in due:
                lane.checking = True
        for lane in due:
            self.checks.submit(self._check, lane)

    # --- lifecycle ---------------------------------------------------------

    def healthy(self) -> bool:
        with self.lock:
            if self.glider is None or self.glider.poll() is not None:
                return False
            ok = {"up"} if self.settings.ip_check_interval else {"running"}
            return any(l.status in ok for l in self.lanes)

    def run(self) -> None:
        self.prepare()
        log.info("starting %d lane(s) on ports %d-%d", len(self.lanes), self.lanes[0].port, self.lanes[-1].port)
        while not self.stopping.is_set():
            self._tick()
            self._schedule_checks()
            self.stopping.wait(1)

    def stop(self) -> None:
        self.stopping.set()
        self.checks.shutdown(wait=False, cancel_futures=True)
        with self.lock:
            procs = [l.proc for l in self.lanes if l.proc] + ([self.glider] if self.glider else [])
            for proc in procs:
                if proc.poll() is None:
                    proc.terminate()
        deadline = time.monotonic() + 5
        for proc in procs:
            try:
                proc.wait(timeout=max(0.1, deadline - time.monotonic()))
            except subprocess.TimeoutExpired:
                proc.kill()
