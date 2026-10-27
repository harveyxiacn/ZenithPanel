"""Focused no-network tests for the Mihomo dual-stack verifier."""

from __future__ import annotations

import importlib.util
import io
from contextlib import redirect_stdout
from pathlib import Path
import tempfile
import unittest


MODULE_PATH = Path(__file__).with_name("verify_mihomo_dual_stack_subscription.py")
SPEC = importlib.util.spec_from_file_location("mihomo_verifier", MODULE_PATH)
assert SPEC and SPEC.loader
verifier = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(verifier)


class MihomoVerifierTest(unittest.TestCase):
    def test_expected_names_match_generator_ipv4_ipv6_labels(self) -> None:
        self.assertEqual(12, len(verifier.EXPECTED_NODES))
        self.assertIn("clash-dual-vless-reality · IPv4", verifier.EXPECTED_NODES)
        self.assertIn("clash-dual-vless-reality · IPv6", verifier.EXPECTED_NODES)
        self.assertNotIn("clash-dual-vless-reality · IPV4", verifier.EXPECTED_NODES)

    def test_provider_file_rejects_explicit_tls_bypass(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            profile = Path(tmp) / "provider.yaml"
            profile.write_text("proxies:\n  - name: node\n    skip-cert-verify: true\n", encoding="utf-8")
            with self.assertRaisesRegex(verifier.VerificationError, "TLS certificate verification"):
                verifier.reject_insecure_provider_file(profile)

    def test_provider_only_structure_uses_verify_selector_not_top_level_nodes(self) -> None:
        name = "clash-dual-vless-reality · IPv6"
        calls = []
        original = verifier.request_json
        def fake_request(_port, _secret, method, path, payload=None, **_kwargs):
            calls.append((method, path, payload))
            if method == "PUT" and path == "/providers/proxies/zenith-private":
                return 204, None
            if method == "GET" and path == "/providers/proxies/zenith-private":
                return 200, {"proxies": [{"name": name, "type": "Vless"}]}
            if method == "GET" and path == "/proxies/VERIFY":
                return 200, {"name": "VERIFY", "type": "Selector", "now": name}
            raise AssertionError(f"top-level provider node lookup is invalid: {method} {path}")
        with tempfile.TemporaryDirectory() as tmp:
            provider_file = Path(tmp) / "provider.yaml"
            provider_file.write_text("proxies: []\n", encoding="utf-8")
            verifier.request_json = fake_request
            try:
                self.assertEqual({name: {"name": name, "type": "Vless"}}, verifier.provider_proxies(1, "private", provider_file))
            finally:
                verifier.request_json = original
        self.assertFalse(any(path == "/proxies" for _, path, _ in calls))

    def test_provider_types_are_checked_before_delay(self) -> None:
        provider = {
            name: {"name": name, "type": protocol}
            for name, (protocol, _family) in verifier.EXPECTED_NODES.items()
        }
        original = verifier.request_json
        current = [next(iter(provider))]
        def fake_request_with_selection(_port, _secret, method, path, payload=None, **_kwargs):
            if method == "PUT" and path == "/proxies/VERIFY":
                current[0] = payload["name"]
                return 204, None
            if method == "GET" and path == "/proxies/VERIFY":
                return 200, {"type": "Selector", "now": current[0]}
            if method == "GET" and path.startswith("/proxies/VERIFY/delay?"):
                return 200, {"delay": 12}
            raise AssertionError(f"unexpected controller request: {method} {path}")
        verifier.request_json = fake_request_with_selection
        try:
            with redirect_stdout(io.StringIO()):
                self.assertTrue(verifier.validate_nodes(provider, 1, "private", "https://example.test/204"))
            provider[next(iter(provider))]["type"] = "wrong"
            with redirect_stdout(io.StringIO()):
                self.assertFalse(verifier.validate_nodes(provider, 1, "private", "https://example.test/204"))
        finally:
            verifier.request_json = original

    def test_selector_mismatch_is_not_accepted_as_the_requested_provider_node(self) -> None:
        provider = {
            name: {"name": name, "type": protocol}
            for name, (protocol, _family) in verifier.EXPECTED_NODES.items()
        }
        original = verifier.request_json
        delay_calls = []

        def fake_request(_port, _secret, method, path, payload=None, **_kwargs):
            if method == "PUT" and path == "/proxies/VERIFY":
                return 204, None
            if method == "GET" and path == "/proxies/VERIFY":
                return 200, {"type": "Selector", "now": "a-different-provider"}
            if method == "GET" and path.startswith("/proxies/VERIFY/delay?"):
                delay_calls.append((method, path, payload))
                return 200, {"delay": 12}
            raise AssertionError(f"unexpected controller request: {method} {path}")

        verifier.request_json = fake_request
        try:
            with redirect_stdout(io.StringIO()):
                self.assertFalse(verifier.validate_nodes(provider, 1, "private", "https://example.test/204"))
        finally:
            verifier.request_json = original
        self.assertEqual([], delay_calls)

    def test_failed_selector_delay_is_not_reported_as_a_pass(self) -> None:
        provider = {
            name: {"name": name, "type": protocol}
            for name, (protocol, _family) in verifier.EXPECTED_NODES.items()
        }
        original = verifier.request_json
        current = [next(iter(provider))]

        def fake_request(_port, _secret, method, path, payload=None, **_kwargs):
            if method == "PUT" and path == "/proxies/VERIFY":
                current[0] = payload["name"]
                return 204, None
            if method == "GET" and path == "/proxies/VERIFY":
                return 200, {"type": "Selector", "now": current[0]}
            if method == "GET" and path.startswith("/proxies/VERIFY/delay?"):
                return 500, {"message": "probe failed"}
            raise AssertionError(f"unexpected controller request: {method} {path}")

        verifier.request_json = fake_request
        try:
            with redirect_stdout(io.StringIO()):
                self.assertFalse(verifier.validate_nodes(provider, 1, "private", "https://example.test/204"))
        finally:
            verifier.request_json = original


if __name__ == "__main__":
    unittest.main()
