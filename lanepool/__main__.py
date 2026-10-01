"""Entry point: python -m lanepool"""

from __future__ import annotations

import logging
import os
import signal
import sys
import threading

from . import __version__
from .api import make_server
from .config import ConfigError, Settings
from .sources import collect_lanes
from .supervisor import Supervisor


def main() -> int:
    logging.basicConfig(
        level=os.environ.get("LOG_LEVEL", "INFO").upper(),
        format="%(asctime)s %(levelname)-7s %(name)s: %(message)s",
        stream=sys.stdout,
    )
    log = logging.getLogger("lanepool")
    log.info("lanepool %s", __version__)

    try:
        settings = Settings.from_env()
        specs = collect_lanes(settings)
    except (ConfigError, ValueError, OSError) as exc:
        log.error("%s", exc)
        return 2

    if not specs:
        log.error(
            "no lanes configured: set SURFSHARK_PRIVATE_KEY or put WireGuard .conf files in %s",
            settings.config_dir,
        )
        return 2
    if len(specs) < settings.lanes:
        log.warning("requested %d lanes, only %d available", settings.lanes, len(specs))

    sup = Supervisor(settings, specs)
    server = make_server(sup)
    threading.Thread(target=server.serve_forever, daemon=True).start()
    log.info("dashboard and API on :%d", settings.api_port)

    def shutdown(signum, _frame):
        log.info("received %s, shutting down", signal.Signals(signum).name)
        sup.stopping.set()

    signal.signal(signal.SIGTERM, shutdown)
    signal.signal(signal.SIGINT, shutdown)

    try:
        sup.run()
    finally:
        sup.stop()
        server.shutdown()
        server.server_close()
    return 0


if __name__ == "__main__":
    sys.exit(main())
