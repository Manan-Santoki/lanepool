"""Runtime settings, read once from environment variables."""

from __future__ import annotations

import os
from dataclasses import dataclass, field
from pathlib import Path


class ConfigError(ValueError):
    pass


def _str(name: str, default: str = "") -> str:
    return os.environ.get(name, default).strip()


def _int(name: str, default: int, minimum: int = 0, maximum: int | None = None) -> int:
    raw = _str(name)
    if not raw:
        return default
    try:
        value = int(raw)
    except ValueError as exc:
        raise ConfigError(f"{name} must be an integer, got {raw!r}") from exc
    if value < minimum or (maximum is not None and value > maximum):
        raise ConfigError(f"{name} must be between {minimum} and {maximum}, got {value}")
    return value


def _float(name: str, default: float, minimum: float = 0.0) -> float:
    raw = _str(name)
    if not raw:
        return default
    try:
        value = float(raw)
    except ValueError as exc:
        raise ConfigError(f"{name} must be a number, got {raw!r}") from exc
    if value < minimum:
        raise ConfigError(f"{name} must be at least {minimum}, got {value}")
    return value


def _bool(name: str, default: bool) -> bool:
    raw = _str(name).lower()
    if not raw:
        return default
    if raw in {"1", "true", "yes", "on"}:
        return True
    if raw in {"0", "false", "no", "off"}:
        return False
    raise ConfigError(f"{name} must be true or false, got {raw!r}")


def _list(name: str) -> list[str]:
    return [item.strip().lower() for item in _str(name).split(",") if item.strip()]


def _keys() -> list[str]:
    """SURFSHARK_PRIVATE_KEYS (comma separated) plus SURFSHARK_PRIVATE_KEY, de-duplicated."""
    raw = _str("SURFSHARK_PRIVATE_KEYS").split(",") + [_str("SURFSHARK_PRIVATE_KEY")]
    keys: list[str] = []
    for key in (k.strip() for k in raw):
        if key and key not in keys:
            keys.append(key)
    return keys


@dataclass(frozen=True)
class Settings:
    # Surfshark (optional: leave the key empty to only use files from CONFIG_DIR)
    surfshark_private_keys: list[str] = field(default_factory=list)
    surfshark_address: str = "10.14.0.2/32"
    surfshark_dns: str = "162.252.172.57, 149.154.159.92"
    surfshark_api: str = "https://api.surfshark.com/v4/server/clusters/generic"
    surfshark_locations: list[str] = field(default_factory=list)
    countries: list[str] = field(default_factory=list)
    exclude_countries: list[str] = field(default_factory=list)
    include_virtual: bool = True

    # Lanes
    lanes: int = 99
    config_dir: Path = Path("/config/wireguard")
    run_dir: Path = Path("/run/lanepool")
    lane_port_start: int = 10001
    lane_bind: str = "0.0.0.0"
    lane_logs: bool = False
    lane_start_delay: float = 1.0
    connect_timeout: int = 90
    retry_backoff: int = 300
    retry_backoff_max: int = 3600

    # Rotating front proxy
    proxy_port: int = 8080
    proxy_user: str = ""
    proxy_pass: str = ""
    strategy: str = "rr"
    check_url: str = "http://www.msftconnecttest.com/connecttest.txt#expect=200"
    check_interval: int = 30
    max_failures: int = 3

    # Status API / exit-IP checks
    api_port: int = 8000
    ip_check_url: str = "http://api.ipify.org/"
    ip_check_interval: int = 600

    @property
    def auth_enabled(self) -> bool:
        return bool(self.proxy_user)

    @classmethod
    def from_env(cls) -> "Settings":
        user, password = _str("PROXY_USER"), _str("PROXY_PASS")
        if bool(user) != bool(password):
            raise ConfigError("set both PROXY_USER and PROXY_PASS, or neither")
        if any(ch.isspace() for ch in user + password):
            raise ConfigError("PROXY_USER and PROXY_PASS must not contain whitespace")

        strategy = _str("STRATEGY", "rr").lower()
        if strategy not in {"rr", "ha", "lha", "dh"}:
            raise ConfigError("STRATEGY must be one of rr, ha, lha, dh")

        ip_check_url = _str("IP_CHECK_URL", cls.ip_check_url)
        if not ip_check_url.startswith("http://"):
            raise ConfigError("IP_CHECK_URL must be a plain http:// URL")

        settings = cls(
            surfshark_private_keys=_keys(),
            surfshark_address=_str("SURFSHARK_ADDRESS", cls.surfshark_address),
            surfshark_dns=_str("SURFSHARK_DNS", cls.surfshark_dns),
            surfshark_api=_str("SURFSHARK_API", cls.surfshark_api),
            surfshark_locations=_list("SURFSHARK_LOCATIONS"),
            countries=_list("COUNTRIES"),
            exclude_countries=_list("EXCLUDE_COUNTRIES"),
            include_virtual=_bool("INCLUDE_VIRTUAL", True),
            lanes=_int("LANES", 99, minimum=1, maximum=1000),
            config_dir=Path(_str("CONFIG_DIR", str(cls.config_dir))),
            run_dir=Path(_str("RUN_DIR", str(cls.run_dir))),
            lane_port_start=_int("LANE_PORT_START", 10001, minimum=1024, maximum=65000),
            lane_bind=_str("LANE_BIND", cls.lane_bind),
            lane_logs=_bool("LANE_LOGS", False),
            lane_start_delay=_float("LANE_START_DELAY", 1.0),
            connect_timeout=_int("CONNECT_TIMEOUT", 90, minimum=15),
            retry_backoff=_int("RETRY_BACKOFF", 300, minimum=5),
            retry_backoff_max=_int("RETRY_BACKOFF_MAX", 3600, minimum=5),
            proxy_port=_int("PROXY_PORT", 8080, minimum=1, maximum=65535),
            proxy_user=user,
            proxy_pass=password,
            strategy=strategy,
            check_url=_str("CHECK_URL", cls.check_url),
            check_interval=_int("CHECK_INTERVAL", 30, minimum=5),
            max_failures=_int("MAX_FAILURES", 3, minimum=1),
            api_port=_int("API_PORT", 8000, minimum=1, maximum=65535),
            ip_check_url=ip_check_url,
            ip_check_interval=_int("IP_CHECK_INTERVAL", 600, minimum=0),
        )
        if settings.lane_port_start + settings.lanes - 1 > 65535:
            raise ConfigError("LANE_PORT_START + LANES exceeds the port range")
        lane_ports = range(settings.lane_port_start, settings.lane_port_start + settings.lanes)
        for name, port in (("PROXY_PORT", settings.proxy_port), ("API_PORT", settings.api_port)):
            if port in lane_ports:
                raise ConfigError(f"{name} {port} collides with the lane port range")
        return settings
