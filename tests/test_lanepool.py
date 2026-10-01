import json
import os
import socket
import tempfile
import time
import unittest
import urllib.request
from pathlib import Path
from unittest import mock

FAKES = Path(__file__).parent / "fakes"
os.environ["WIREPROXY_BIN"] = str(FAKES / "wireproxy")
os.environ["GLIDER_BIN"] = str(FAKES / "glider")

from lanepool import sources, supervisor  # noqa: E402
from lanepool.api import make_server  # noqa: E402
from lanepool.config import ConfigError, Settings  # noqa: E402
from lanepool.sources import LaneSpec  # noqa: E402

KEY = "yAnz5TF+lXXJte14tji3zlMNq+hd2rYUIgJBgB3fBmk="


def server(cc, loc, virtual=False):
    return {
        "country": cc.upper(), "countryCode": cc.upper(), "location": loc,
        "connectionName": f"{cc}-{loc}.prod.surfshark.com", "pubKey": KEY,
        "tags": ["virtual"] if virtual else [],
    }


def free_port_block(n):
    for start in range(20000, 60000, 500):
        socks = []
        try:
            for p in range(start, start + n + 2):
                s = socket.socket()
                socks.append(s)
                s.bind(("127.0.0.1", p))
            return start
        except OSError:
            continue
        finally:
            for s in socks:
                s.close()
    raise RuntimeError("no free ports")


class SettingsTest(unittest.TestCase):
    def env(self, **kw):
        return mock.patch.dict(os.environ, kw, clear=True)

    def test_defaults(self):
        with self.env():
            s = Settings.from_env()
        self.assertEqual(s.lanes, 99)
        self.assertEqual(s.strategy, "rr")
        self.assertFalse(s.auth_enabled)

    def test_auth_needs_both(self):
        with self.env(PROXY_USER="a"), self.assertRaises(ConfigError):
            Settings.from_env()

    def test_bad_strategy(self):
        with self.env(STRATEGY="random"), self.assertRaises(ConfigError):
            Settings.from_env()

    def test_proxy_port_collision(self):
        with self.env(PROXY_PORT="10005"), self.assertRaises(ConfigError):
            Settings.from_env()

    def test_keys_merge_and_dedupe(self):
        with self.env(SURFSHARK_PRIVATE_KEYS=" a, b ,a", SURFSHARK_PRIVATE_KEY="c"):
            self.assertEqual(Settings.from_env().surfshark_private_keys, ["a", "b", "c"])
        with self.env(SURFSHARK_PRIVATE_KEY="a"):
            self.assertEqual(Settings.from_env().surfshark_private_keys, ["a"])

    def test_lists(self):
        with self.env(COUNTRIES="US, de ,", SURFSHARK_LOCATIONS="us-nyc"):
            s = Settings.from_env()
        self.assertEqual(s.countries, ["us", "de"])
        self.assertEqual(s.surfshark_locations, ["us-nyc"])


class SourcesTest(unittest.TestCase):
    servers = [server("us", "nyc"), server("us", "lax"), server("de", "fra"),
               server("de", "ber", virtual=True), server("al", "tia")]

    def test_spreads_across_countries_first(self):
        picked = sources.select_surfshark_servers(self.servers, Settings(), 4)
        self.assertEqual([sources._location_id(s) for s in picked], ["al-tia", "de-ber", "us-lax", "de-fra"])

    def test_filters(self):
        s = Settings(countries=["us", "de"], exclude_countries=["us"], include_virtual=False)
        picked = sources.select_surfshark_servers(self.servers, s, 10)
        self.assertEqual([sources._location_id(x) for x in picked], ["de-fra"])

    def test_pinned_locations_keep_order(self):
        s = Settings(surfshark_locations=["us-nyc", "nope", "al-tia"])
        picked = sources.select_surfshark_servers(self.servers, s, 10)
        self.assertEqual([sources._location_id(x) for x in picked], ["us-nyc", "al-tia"])

    def test_surfshark_config(self):
        text = sources.surfshark_wg_config(self.servers[0], Settings(), KEY)
        self.assertIn(f"PrivateKey = {KEY}", text)
        self.assertIn("Endpoint = us-nyc.prod.surfshark.com:51820", text)
        self.assertIn("Address = 10.14.0.2/32", text)

    def test_key_validation(self):
        self.assertTrue(sources.valid_wireguard_key(KEY))
        self.assertFalse(sources.valid_wireguard_key("not-a-key"))

    def test_file_lanes_normalise(self):
        with tempfile.TemporaryDirectory() as d:
            Path(d, "My Server.conf").write_text(
                "[Interface]\nPrivateKey = abc\nAddress = 10.0.0.2/16, fd00::2/64\nDNS = 1.1.1.1\n"
                "PostUp = iptables -A ...\n\n[Peer]\nPublicKey = def\nEndpoint = 1.2.3.4:51820\n"
            )
            Path(d, "broken.conf").write_text("[Interface]\n")
            lanes = sources.file_lanes(Path(d), 10)
        self.assertEqual(len(lanes), 1)
        self.assertEqual(lanes[0].name, "my-server")
        self.assertIn("Address = 10.0.0.2/32, fd00::2/128", lanes[0].wg_config)
        self.assertIn("AllowedIPs = 0.0.0.0/0", lanes[0].wg_config)
        self.assertNotIn("PostUp", lanes[0].wg_config)

    def test_collect_files_then_surfshark_with_unique_names(self):
        with tempfile.TemporaryDirectory() as d:
            Path(d, "us-nyc.conf").write_text(
                "[Interface]\nPrivateKey = a\nAddress = 10.0.0.2/32\n[Peer]\nPublicKey = b\nEndpoint = x:1\n"
            )
            s = Settings(config_dir=Path(d), surfshark_private_keys=[KEY], lanes=3)
            with mock.patch.object(sources, "fetch_surfshark_servers", return_value=self.servers):
                lanes = sources.collect_lanes(s)
        self.assertEqual([l.name for l in lanes], ["us-nyc", "al-tia", "de-ber"])

    def test_lanes_spread_round_robin_across_keys(self):
        key2 = "aAnz5TF+lXXJte14tji3zlMNq+hd2rYUIgJBgB3fBmk="
        s = Settings(config_dir=Path("/nonexistent"), surfshark_private_keys=[KEY, key2], lanes=5)
        with mock.patch.object(sources, "fetch_surfshark_servers", return_value=self.servers):
            lanes = sources.collect_lanes(s)
        self.assertEqual([l.key_slot for l in lanes], [0, 1, 0, 1, 0])
        self.assertIn(f"PrivateKey = {key2}", lanes[1].wg_config)
        self.assertIn(f"PrivateKey = {KEY}", lanes[2].wg_config)

    def test_collect_rejects_bad_key(self):
        s = Settings(config_dir=Path("/nonexistent"), surfshark_private_keys=["bad"])
        with self.assertRaises(ValueError):
            sources.collect_lanes(s)


class GliderConfigTest(unittest.TestCase):
    def test_auth_is_url_encoded(self):
        s = Settings(proxy_user="me", proxy_pass="p@ss:w/rd")
        lanes = [supervisor.Lane(index=0, port=10001, spec=LaneSpec("a", "file", ""))]
        text = supervisor.glider_config(lanes, s)
        self.assertIn("listen=mixed://me:p%40ss%3Aw%2Frd@:8080", text)
        self.assertIn("forward=socks5://me:p%40ss%3Aw%2Frd@127.0.0.1:10001", text)


class EndToEndTest(unittest.TestCase):
    """Runs the supervisor with fake wireproxy/glider binaries."""

    def run_supervisor(self, n_lanes, user="", password="", fail=(), dead=(), **extra):
        start = free_port_block(n_lanes + 2)
        tmp = tempfile.TemporaryDirectory()
        self.addCleanup(tmp.cleanup)
        settings = Settings(
            lanes=n_lanes, run_dir=Path(tmp.name), lane_bind="127.0.0.1",
            lane_port_start=start, proxy_port=start + n_lanes, api_port=start + n_lanes + 1,
            proxy_user=user, proxy_pass=password, ip_check_interval=60,
            **{"lane_start_delay": 0.1, **extra},
        )
        def name(i):
            return f"lane{i}" + ("-FAIL" if i in fail else "") + ("-DEAD" if i in dead else "")
        specs = [LaneSpec(name(i), "file", "x") for i in range(n_lanes)]
        sup = supervisor.Supervisor(settings, specs)
        api = make_server(sup)
        import threading
        threading.Thread(target=sup.run, daemon=True).start()
        threading.Thread(target=api.serve_forever, daemon=True).start()
        self.addCleanup(api.server_close)
        self.addCleanup(api.shutdown)
        self.addCleanup(sup.stop)
        return sup, settings

    def wait_for(self, cond, timeout=20):
        deadline = time.time() + timeout
        while time.time() < deadline:
            if cond():
                return
            time.sleep(0.2)
        self.fail("condition not met in time")

    def test_lanes_come_up_with_exit_ips(self):
        sup, s = self.run_supervisor(3, user="u", password="p")
        self.wait_for(lambda: all(l.status == "up" for l in sup.lanes))
        self.assertEqual(len({l.exit_ip for l in sup.lanes}), 3)
        self.assertTrue(sup.healthy())

        base = f"http://127.0.0.1:{s.api_port}"
        self.assertEqual(urllib.request.urlopen(base + "/healthz").status, 200)
        with self.assertRaises(urllib.error.HTTPError) as ctx:
            urllib.request.urlopen(base + "/api/status")
        self.assertEqual(ctx.exception.code, 401)

        req = urllib.request.Request(base + "/api/status", headers={"Authorization": "Basic dTpw"})
        status = json.load(urllib.request.urlopen(req))
        self.assertEqual(status["lanes_total"], 3)
        self.assertEqual(status["unique_exit_ips"], 3)

        # The fake glider doesn't proxy, so every attempt reports an error rather than raising.
        req = urllib.request.Request(base + "/api/rotation-test", headers={"Authorization": "Basic dTpw"})
        test = json.load(urllib.request.urlopen(req))
        self.assertEqual((test["attempts"], test["ok"]), (6, 0))
        self.assertTrue(all("error" in r for r in test["results"]))

    def test_crashing_lane_is_restarted_and_others_stay_up(self):
        sup, _ = self.run_supervisor(2, fail={1})
        self.wait_for(lambda: sup.lanes[0].status == "up" and sup.lanes[1].restarts >= 2)
        self.assertIn("simulated failure", sup.lanes[1].last_error)
        self.assertIn(sup.lanes[1].status, {"restarting", "starting"})

    def test_lane_that_never_connects_is_parked(self):
        sup, _ = self.run_supervisor(2, dead={1}, connect_timeout=3, retry_backoff=600)
        # Two failed checks are needed: at +5s and then 15s later.
        self.wait_for(lambda: sup.lanes[0].status == "up" and sup.lanes[1].status == "backoff", timeout=40)
        dead = sup.lanes[1]
        self.wait_for(lambda: dead.proc is None)
        self.assertIn("retrying in 10 min", dead.last_error)
        self.assertEqual(dead.restarts, 0)  # parking is not counted as a crash
        time.sleep(2)
        self.assertEqual(dead.status, "backoff")  # stays off until the backoff expires

    def test_starts_are_gated_one_at_a_time(self):
        sup, _ = self.run_supervisor(3, dead={0, 1, 2}, max_connecting=1, lane_start_delay=0,
                                     connect_timeout=3, retry_backoff=600, breaker_pause=0)
        seen_connecting = 0
        # Sequential: each dead lane takes ~21s (two failed checks) before the next may start.
        deadline = time.time() + 120
        while time.time() < deadline and not all(l.status == "backoff" for l in sup.lanes):
            with sup.lock:
                seen_connecting = max(seen_connecting, sum(1 for l in sup.lanes if l.status == "starting"))
            time.sleep(0.2)
        self.assertTrue(all(l.status == "backoff" for l in sup.lanes))
        self.assertEqual(seen_connecting, 1)

    def test_failed_lane_retries_with_next_key(self):
        key2 = "aAnz5TF+lXXJte14tji3zlMNq+hd2rYUIgJBgB3fBmk="
        sup, s = self.run_supervisor(1, dead={0}, connect_timeout=3, retry_backoff=600,
                                     surfshark_private_keys=[KEY, key2])
        lane = sup.lanes[0]
        lane.spec.source, lane.spec.server, lane.spec.key_slot = "surfshark", server("us", "nyc"), 0
        self.wait_for(lambda: lane.status == "backoff", timeout=40)
        self.assertEqual(lane.spec.key_slot, 1)
        self.assertIn("with key 2", lane.last_error)
        wg = (s.run_dir / "lanes" / f"000-{lane.name}.wg.conf").read_text()
        self.assertIn(f"PrivateKey = {key2}", wg)

    def test_breaker_pauses_new_connections_but_keeps_working_lanes(self):
        sup, _ = self.run_supervisor(4, dead={1, 2, 3}, max_connecting=4, lane_start_delay=0,
                                     connect_timeout=3, retry_backoff=600,
                                     breaker_failures=2, breaker_pause=600)
        self.wait_for(lambda: sup.pause_remaining() > 0, timeout=40)
        self.assertEqual(sup.lanes[0].status, "up")
        # The third dead lane may already have been connecting; nothing new starts during the pause.
        with sup.lock:
            queued = [l for l in sup.lanes if l.status == "queued"]
        time.sleep(3)
        self.assertTrue(all(l.status == "queued" for l in queued))

    def test_restart_via_api(self):
        sup, s = self.run_supervisor(1)
        self.wait_for(lambda: sup.lanes[0].status == "up")
        req = urllib.request.Request(
            f"http://127.0.0.1:{s.api_port}/api/lanes/lane0/restart", method="POST"
        )
        self.assertEqual(urllib.request.urlopen(req).status, 202)
        self.wait_for(lambda: sup.lanes[0].restarts == 1 and sup.lanes[0].status == "up")


if __name__ == "__main__":
    unittest.main()
