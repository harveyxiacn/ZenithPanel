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

    def test_provider_types_are_checked_before_delay(self) -> None:
        provider = {
            name: {"name": name, "type": protocol}
            for name, (protocol, _family) in verifier.EXPECTED_NODES.items()
        }
        controller = {name: {"name": name, "type": value[0]} for name, value in verifier.EXPECTED_NODES.items()}
        original = verifier.request_json
        verifier.request_json = lambda *_args, **_kwargs: (200, {"delay": 12})
        try:
            with redirect_stdout(io.StringIO()):
                self.assertTrue(verifier.validate_nodes(provider, controller, 1, "private", "https://example.test/204"))
            provider[next(iter(provider))]["type"] = "wrong"
            with redirect_stdout(io.StringIO()):
                self.assertFalse(verifier.validate_nodes(provider, controller, 1, "private", "https://example.test/204"))
        finally:
            verifier.request_json = original


if __name__ == "__main__":
    unittest.main()
