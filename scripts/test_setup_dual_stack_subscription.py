"""Focused non-Docker tests for setup_dual_stack_subscription.py."""

from __future__ import annotations

import copy
import importlib.util
import io
from contextlib import redirect_stdout
from pathlib import Path
import tempfile
import unittest


MODULE_PATH = Path(__file__).with_name("setup_dual_stack_subscription.py")
SPEC = importlib.util.spec_from_file_location("setup_dual_stack_subscription", MODULE_PATH)
assert SPEC and SPEC.loader
provision = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(provision)


class MockPanelAPI:
    def __init__(self) -> None:
        self.log_path = Path("/root/private/api-transcript.log")
        self.inbounds: list[dict] = []
        self.clients: list[dict] = []
        self.calls: list[tuple[str, str, object]] = []
        self.next_inbound_id = 10
        self.next_client_id = 100
        self.subscription = {"enabled": False, "port": 2096, "public_host": "", "cert_domain": ""}
        self.fail_probe = False
        self.firewall_rules: dict[str, list[dict]] | None = None

    def readable_file(self, _path: str) -> bool:
        return True

    def call(self, method: str, path: str, payload=None):
        self.calls.append((method, path, copy.deepcopy(payload)))
        if method == "GET" and path == "/api/v1/inbounds":
            return copy.deepcopy(self.inbounds)
        if method == "POST" and path == "/api/v1/proxy/generate-reality-keys":
            return {"private_key": "private", "public_key": "public", "short_id": "abcd"}
        if method == "POST" and path == "/api/v1/inbounds":
            created = copy.deepcopy(payload)
            created["id"] = self.next_inbound_id
            self.next_inbound_id += 1
            self.inbounds.append(created)
            return copy.deepcopy(created)
        if method == "GET" and path.startswith("/api/v1/clients?inbound_id="):
            inbound_id = int(path.rsplit("=", 1)[1])
            return copy.deepcopy([c for c in self.clients if c["inbound_id"] == inbound_id])
        if method == "POST" and path == "/api/v1/clients":
            created = copy.deepcopy(payload)
            created["id"] = self.next_client_id
            self.next_client_id += 1
            self.clients.append(created)
            return copy.deepcopy(created)
        if method == "POST" and path == "/api/v1/proxy/apply":
            return {}
        if method == "GET" and path.startswith("/api/v1/proxy/test/"):
            return {"ok": not self.fail_probe, "e2e": "failed" if self.fail_probe else "passed"}
        if method == "GET" and path == "/api/v1/admin/subscription":
            return copy.deepcopy(self.subscription)
        if method == "PUT" and path == "/api/v1/admin/subscription":
            self.subscription = {
                "enabled": payload["enabled"], "port": payload["port"],
                "public_host": payload["public_host"], "cert_domain": payload["cert_domain"],
                "running": True, "tls": True,
                "base_url": "https://example.test:2096/secret/",
            }
            return {"status": copy.deepcopy(self.subscription)}
        if method == "GET" and path.startswith("/api/v1/firewall/rules?"):
            if self.firewall_rules is not None:
                family = path.rsplit("=", 1)[1]
                return copy.deepcopy(self.firewall_rules.get(family, []))
            return [{"target": "ACCEPT", "protocol": proto, "port": port} for proto, port in {
                ("tcp", "443"), ("tcp", "31402"), ("tcp", "31403"), ("tcp", "31404"),
                ("udp", "31404"), ("udp", "8443"), ("udp", "31406"), ("tcp", "2096"),
            }]
        raise AssertionError(f"unexpected API call {method} {path}")


def args() -> object:
    return provision.parse_args([
        "--ipv4", "64.186.238.234", "--ipv6", "2605:52c0:2:3f30:be24:11ff:fece:71b",
        "--cert-domain", "64-186-238-234.2605-52c0-2-3f30-be24-11ff-fece-71b.sslip.io",
        "--cert-path", "/opt/zenithpanel/data/certs/64-186-238-234.2605-52c0-2-3f30-be24-11ff-fece-71b.sslip.io.crt",
        "--key-path", "/opt/zenithpanel/data/certs/64-186-238-234.2605-52c0-2-3f30-be24-11ff-fece-71b.sslip.io.key",
        "--subscription-public-host", "64-186-238-234.2605-52c0-2-3f30-be24-11ff-fece-71b.sslip.io",
    ])


class SetupDualStackSubscriptionTest(unittest.TestCase):
    def test_ufw_parser_requires_active_and_unrestricted_family_rules(self) -> None:
        original = provision.ufw_status_runner
        try:
            provision.ufw_status_runner = lambda: (0, """Status: active

To                         Action      From
--                         ------      ----
443/tcp                    ALLOW       Anywhere                   # ZenithPanel dual-stack
8443/udp (v6)              ALLOW       Anywhere (v6)       # ZenithPanel dual-stack
31402/tcp                 ALLOW       203.0.113.5
31403/tcp (v6)            ALLOW       Anywhere (v6) on eth0
31405/tcp on eth0         ALLOW       Anywhere
31405/tcp (v6) on eth0    ALLOW       Anywhere (v6)
""")
            self.assertEqual(
                {"tcp/443"},
                {f"{protocol}/{port}" for protocol, port in provision.ufw_public_accepts()["ipv4"]},
            )
            self.assertEqual(
                {"udp/8443"},
                {f"{protocol}/{port}" for protocol, port in provision.ufw_public_accepts()["ipv6"]},
            )
            provision.ufw_status_runner = lambda: (0, "Status: inactive\n")
            self.assertEqual({"ipv4": set(), "ipv6": set()}, provision.ufw_public_accepts())
        finally:
            provision.ufw_status_runner = original

    def test_preflight_accepts_unrestricted_ufw_rules_when_panel_rules_missing(self) -> None:
        api = MockPanelAPI()
        api.firewall_rules = {"ipv4": [], "ipv6": []}
        ufw_output = """Status: active

To                         Action      From
--                         ------      ----
443/tcp                    ALLOW       Anywhere
31402/tcp                  ALLOW       Anywhere
31403/tcp                  ALLOW       Anywhere
31404/tcp                  ALLOW       Anywhere
31404/udp                  ALLOW       Anywhere
8443/udp                   ALLOW       Anywhere
31406/udp                  ALLOW       Anywhere
2096/tcp                   ALLOW       Anywhere
443/tcp (v6)              ALLOW       Anywhere (v6)
31402/tcp (v6)            ALLOW       Anywhere (v6)
31403/tcp (v6)            ALLOW       Anywhere (v6)
31404/tcp (v6)            ALLOW       Anywhere (v6)
31404/udp (v6)            ALLOW       Anywhere (v6)
8443/udp (v6)             ALLOW       Anywhere (v6)
31406/udp (v6)            ALLOW       Anywhere (v6)
2096/tcp (v6)             ALLOW       Anywhere (v6)
"""
        original = provision.ufw_status_runner
        try:
            provision.ufw_status_runner = lambda: (0, ufw_output)
            self.assertEqual([], provision.preflight(api, args(), None))
        finally:
            provision.ufw_status_runner = original

    def test_idempotent_resources_are_not_posted_twice(self) -> None:
        api = MockPanelAPI()
        state = provision.initial_state(args())
        with tempfile.TemporaryDirectory() as tmp:
            state_path = Path(tmp) / "state.json"
            provision.ensure_inbounds_and_clients(api, state_path, state)
            first_posts = [call for call in api.calls if call[0] == "POST" and call[1] in {"/api/v1/inbounds", "/api/v1/clients"}]
            self.assertEqual(12, len(first_posts))
            provision.ensure_inbounds_and_clients(api, state_path, state)
            all_posts = [call for call in api.calls if call[0] == "POST" and call[1] in {"/api/v1/inbounds", "/api/v1/clients"}]
            self.assertEqual(first_posts, all_posts)
            self.assertEqual(6, len(api.inbounds))
            self.assertEqual(6, len(api.clients))

    def test_preflight_rejects_foreign_prefixed_tag(self) -> None:
        api = MockPanelAPI()
        api.inbounds = [{"tag": "clash-dual-vmess-ws", "enable": True, "port": 31402}]
        with self.assertRaisesRegex(provision.ProvisioningError, "tag collision"):
            provision.preflight(api, args(), None)
        self.assertFalse(any(method == "POST" for method, _, _ in api.calls))

    def test_existing_client_with_same_email_but_other_uuid_is_rejected(self) -> None:
        api = MockPanelAPI()
        state = provision.initial_state(args())
        state["reality"] = {"private_key": "private", "public_key": "public", "short_id": "abcd"}
        state["hysteria2_obfs_password"] = "obfs"
        payload = provision.inbound_payloads(state)[provision.TAG_ORDER[0]]
        actual = copy.deepcopy(payload)
        actual["id"] = 10
        api.inbounds = [actual]
        state["inbounds"][provision.TAG_ORDER[0]] = {"id": 10, "payload": payload, "client": None}
        plan = provision.client_plan(provision.TAG_ORDER[0], 10, state["shared_uuid"])
        api.clients = [{"id": 100, **plan, "uuid": "different-uuid"}]
        with tempfile.TemporaryDirectory() as tmp:
            with self.assertRaisesRegex(provision.ProvisioningError, "client collision"):
                provision.ensure_inbounds_and_clients(api, Path(tmp) / "state.json", state)
        self.assertFalse(any(method == "POST" and path == "/api/v1/clients" for method, path, _ in api.calls))

    def test_failed_probe_never_configures_or_writes_subscription_url(self) -> None:
        api = MockPanelAPI()
        api.fail_probe = True
        state = provision.initial_state(args())
        state["inbounds"] = {tag: {"id": index + 1} for index, tag in enumerate(provision.TAG_ORDER)}
        with tempfile.TemporaryDirectory() as tmp:
            state_path = Path(tmp) / "state.json"
            with self.assertRaisesRegex(provision.ProvisioningError, "did not pass"):
                provision.apply_and_verify(api, state_path, state)
            self.assertFalse((Path(tmp) / provision.LINK_NAME).exists())
        self.assertFalse(any(path == "/api/v1/admin/subscription" for _, path, _ in api.calls))

    def test_enabled_different_subscription_server_is_not_overwritten(self) -> None:
        api = MockPanelAPI()
        api.subscription = {"enabled": True, "port": 443, "public_host": "other.example", "cert_domain": "other.example"}
        state = provision.initial_state(args())
        with tempfile.TemporaryDirectory() as tmp:
            with self.assertRaisesRegex(provision.ProvisioningError, "refusing to override"):
                provision.configure_subscription_server(api, Path(tmp) / "state.json", state)
        self.assertFalse(any(method == "PUT" and path == "/api/v1/admin/subscription" for method, path, _ in api.calls))

    def test_preflight_rejects_different_enabled_subscription_before_mutations(self) -> None:
        api = MockPanelAPI()
        api.subscription = {"enabled": True, "port": 443, "public_host": "other.example", "cert_domain": "other.example"}
        with self.assertRaisesRegex(provision.ProvisioningError, "refusing to override"):
            provision.preflight(api, args(), None)
        self.assertFalse(any(method == "POST" for method, _, _ in api.calls))

    def test_dry_run_has_no_docker_calls_and_describes_six_resources(self) -> None:
        output = io.StringIO()
        argv = [
            "--dry-run", "--ipv4", "64.186.238.234", "--ipv6", "2605:52c0:2:3f30:be24:11ff:fece:71b",
            "--cert-domain", "64-186-238-234.2605-52c0-2-3f30-be24-11ff-fece-71b.sslip.io",
            "--cert-path", "/opt/zenithpanel/data/certs/64-186-238-234.2605-52c0-2-3f30-be24-11ff-fece-71b.sslip.io.crt",
            "--key-path", "/opt/zenithpanel/data/certs/64-186-238-234.2605-52c0-2-3f30-be24-11ff-fece-71b.sslip.io.key",
            "--subscription-public-host", "64-186-238-234.2605-52c0-2-3f30-be24-11ff-fece-71b.sslip.io",
        ]
        with redirect_stdout(output):
            self.assertEqual(0, provision.main(argv))
        self.assertIn("clash-dual-vless-reality", output.getvalue())
        self.assertIn("listen on ::", output.getvalue())


if __name__ == "__main__":
    unittest.main()
