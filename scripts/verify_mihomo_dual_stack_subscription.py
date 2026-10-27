#!/usr/bin/env python3
"""Verify the twelve nodes in a private Zenith dual-stack Clash subscription.

Run on Linux as root, after ``setup_dual_stack_subscription.py`` has produced
the root-private ``clash-subscription.url`` file.  This script starts one
temporary Mihomo process with controller and mixed ports bound to loopback.  It
does not modify the panel, its firewall, or the permanent Mihomo home.

Only sanitized protocol, address-family, delay and pass/fail rows are printed.
The subscription URL, provider YAML, controller secret and Mihomo stderr stay
inside a mode-0700 temporary directory.  Successful runs remove it; failed
runs retain it under a root-private evidence directory for diagnosis.
"""

from __future__ import annotations

import argparse
import http.client
import json
import os
from pathlib import Path
import re
import secrets
import shutil
import socket
import stat
import subprocess
import sys
import tempfile
import time
from typing import Any
from urllib.parse import quote, urlparse


PROTOCOLS = (
    ("vless", "clash-dual-vless-reality"),
    ("vmess", "clash-dual-vmess-ws"),
    ("trojan", "clash-dual-trojan"),
    ("shadowsocks", "clash-dual-shadowsocks"),
    ("hysteria2", "clash-dual-hysteria2"),
    ("tuic", "clash-dual-tuic"),
)
FAMILIES = (("ipv4", "IPv4"), ("ipv6", "IPv6"))
EXPECTED_NODES = {
    f"{tag} · {family_label}": (protocol, family)
    for protocol, tag in PROTOCOLS
    for family, family_label in FAMILIES
}
DEFAULT_TEST_URL = "https://www.gstatic.com/generate_204"
DEFAULT_EVIDENCE_DIR = Path("/root/zenith-mihomo-evidence")
INSECURE_TLS_LINE = re.compile(
    r"(?im)^\s*(?:skip-cert-verify|allow-insecure|insecure)\s*:\s*[\"']?true[\"']?\s*(?:#.*)?$"
)


class VerificationError(RuntimeError):
    pass


def private_file(path: Path) -> None:
    if path.is_symlink() or not path.is_file():
        raise VerificationError(f"private URL file {path} must be a regular file")
    metadata = path.stat()
    if metadata.st_uid != os.geteuid():
        raise VerificationError(f"private URL file {path} must be owned by the current user")
    if stat.S_IMODE(metadata.st_mode) & 0o077:
        raise VerificationError(f"private URL file {path} must not be group/world accessible")


def read_private_url(path: Path) -> str:
    private_file(path)
    lines = path.read_text(encoding="utf-8").splitlines()
    if len(lines) != 1 or not lines[0].strip():
        raise VerificationError("private URL file must contain exactly one non-empty URL")
    value = lines[0].strip()
    parsed = urlparse(value)
    if parsed.scheme != "https" or not parsed.netloc or not parsed.path:
        raise VerificationError("subscription URL must be an HTTPS URL with a private path")
    return value


def free_loopback_port() -> int:
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as listener:
        listener.bind(("127.0.0.1", 0))
        return int(listener.getsockname()[1])


def yaml_quote(value: str) -> str:
    return "'" + value.replace("'", "''") + "'"


def write_config(path: Path, subscription_url: str, controller_port: int, mixed_port: int, secret: str) -> None:
    # The provider is intentionally HTTP(S), rather than an inline `proxies`
    # block: this makes Mihomo parse the exact document that clients import.
    # `secret` protects the loopback controller as defence in depth.
    config = "\n".join((
        "allow-lan: false",
        "bind-address: 127.0.0.1",
        "mode: rule",
        "ipv6: true",
        "log-level: warning",
        f"mixed-port: {mixed_port}",
        f"external-controller: 127.0.0.1:{controller_port}",
        f"secret: {yaml_quote(secret)}",
        "proxy-providers:",
        "  zenith-private:",
        "    type: http",
        f"    url: {yaml_quote(subscription_url)}",
        "    path: ./zenith-private-provider.yaml",
        "    interval: 0",
        "    health-check:",
        "      enable: false",
        "proxy-groups:",
        "  - name: VERIFY",
        "    type: select",
        "    use:",
        "      - zenith-private",
        "rules:",
        "  - MATCH,VERIFY",
        "",
    ))
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    with os.fdopen(fd, "w", encoding="utf-8") as handle:
        handle.write(config)


def reject_insecure_provider_file(path: Path) -> None:
    """Reject an explicit TLS-verification bypass in the fetched profile.

    Controller proxy objects do not consistently include their TLS settings.
    The downloaded provider document is therefore the authoritative check for
    the explicit booleans Zenith's generated YAML could contain.
    """
    try:
        provider_text = path.read_text(encoding="utf-8")
    except OSError as exc:
        raise VerificationError("Mihomo did not retain the downloaded private provider file") from exc
    if INSECURE_TLS_LINE.search(provider_text):
        raise VerificationError("private provider explicitly disables TLS certificate verification")


def request_json(port: int, secret: str, method: str, path: str, payload: Any | None = None, timeout: float = 8.0) -> tuple[int, Any]:
    connection = http.client.HTTPConnection("127.0.0.1", port, timeout=timeout)
    headers = {"Authorization": "Bearer " + secret}
    body = None
    if payload is not None:
        headers["Content-Type"] = "application/json"
        body = json.dumps(payload, separators=(",", ":"))
    try:
        connection.request(method, path, body=body, headers=headers)
        response = connection.getresponse()
        raw = response.read()
    finally:
        connection.close()
    if not raw:
        return response.status, None
    try:
        return response.status, json.loads(raw)
    except json.JSONDecodeError as exc:
        raise VerificationError(f"Mihomo controller returned non-JSON for {method} {path}") from exc


def require_mihomo_flags(binary: Path) -> None:
    try:
        result = subprocess.run([str(binary), "-h"], text=True, capture_output=True, timeout=15)
    except (OSError, subprocess.TimeoutExpired) as exc:
        raise VerificationError("could not execute Mihomo --help") from exc
    help_text = result.stdout + result.stderr
    missing = [flag for flag in ("-d", "-f", "-ext-ctl", "-secret") if flag not in help_text]
    if missing:
        raise VerificationError("Mihomo binary lacks required CLI flags: " + ", ".join(missing))


def wait_for_controller(port: int, secret: str, process: subprocess.Popen[str], timeout: float = 35.0) -> None:
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        if process.poll() is not None:
            raise VerificationError("temporary Mihomo process exited before controller became ready")
        try:
            status, _ = request_json(port, secret, "GET", "/version", timeout=1.0)
            if status == 200:
                return
        except (ConnectionError, OSError, http.client.HTTPException, VerificationError):
            pass
        time.sleep(0.25)
    raise VerificationError("timed out waiting for temporary Mihomo controller")


def provider_proxies(port: int, secret: str, provider_file: Path) -> dict[str, dict[str, Any]]:
    status, _ = request_json(port, secret, "PUT", "/providers/proxies/zenith-private", timeout=20.0)
    if status not in (200, 204):
        raise VerificationError("Mihomo could not refresh the private proxy provider")
    deadline = time.monotonic() + 35.0
    while time.monotonic() < deadline:
        status, body = request_json(port, secret, "GET", "/providers/proxies/zenith-private")
        if status == 200 and isinstance(body, dict) and isinstance(body.get("proxies"), list):
            provider = {entry.get("name"): entry for entry in body["proxies"] if isinstance(entry, dict) and isinstance(entry.get("name"), str)}
            if provider:
                reject_insecure_provider_file(provider_file)
                # Real Mihomo releases can omit provider nodes from top-level
                # /proxies; they expose the VERIFY selector there instead.
                selector_status, selector = request_json(port, secret, "GET", "/proxies/VERIFY")
                if selector_status == 200 and isinstance(selector, dict) and str(selector.get("type", "")).lower() == "selector":
                    return provider
        time.sleep(0.25)
    raise VerificationError("Mihomo did not load any proxies from the private subscription")


def is_insecure_proxy(proxy: dict[str, Any]) -> bool:
    # This is a supplementary guard only.  The provider-file check above is
    # authoritative because controller responses may omit TLS configuration.
    stack: list[Any] = [proxy]
    while stack:
        value = stack.pop()
        if isinstance(value, dict):
            for key, nested in value.items():
                normalized = key.lower().replace("_", "-")
                if normalized in {"skip-cert-verify", "allow-insecure", "insecure"} and nested is True:
                    return True
                stack.append(nested)
        elif isinstance(value, list):
            stack.extend(value)
    return False


def print_result(protocol: str, family: str, delay: int | str, passed: bool) -> None:
    print(f"{protocol:12} {family:4} {str(delay):>6} {'PASS' if passed else 'FAIL'}")


def select_and_confirm(port: int, secret: str, name: str) -> bool:
    status, _ = request_json(port, secret, "PUT", "/proxies/VERIFY", {"name": name})
    if status != 204:
        return False
    status, selector = request_json(port, secret, "GET", "/proxies/VERIFY")
    return status == 200 and isinstance(selector, dict) and selector.get("now") == name


def validate_nodes(provider: dict[str, dict[str, Any]], port: int, secret: str, test_url: str) -> bool:
    expected_names = set(EXPECTED_NODES)
    actual_names = set(provider)
    success = True
    if actual_names != expected_names:
        # Do not print a provider-supplied node name; it can contain operator
        # labels.  The expected rows below identify only protocol/family.
        success = False
    for name, (protocol, family) in EXPECTED_NODES.items():
        provider_proxy = provider.get(name)
        provider_type = str(provider_proxy.get("type", "")).lower() if isinstance(provider_proxy, dict) else ""
        if provider_type != protocol or not isinstance(provider_proxy, dict) or is_insecure_proxy(provider_proxy):
            print_result(protocol, family, "-", False)
            success = False
            continue
        try:
            if not select_and_confirm(port, secret, name):
                raise VerificationError("selector did not retain the requested provider node")
            path = "/proxies/VERIFY/delay?url=" + quote(test_url, safe="") + "&timeout=15000&expected=204"
            status, body = request_json(port, secret, "GET", path, timeout=22.0)
            delay = body.get("delay") if isinstance(body, dict) else None
            passed = status == 200 and isinstance(delay, int) and delay >= 0
        except (ConnectionError, OSError, http.client.HTTPException, VerificationError):
            delay, passed = "-", False
        print_result(protocol, family, delay if passed else "-", passed)
        success = success and passed
    return success


def stop_process(process: subprocess.Popen[str] | None) -> None:
    if process is None or process.poll() is not None:
        return
    process.terminate()
    try:
        process.wait(timeout=8)
    except subprocess.TimeoutExpired:
        process.kill()
        process.wait(timeout=5)


def retain_evidence(work: Path, evidence_dir: Path) -> Path:
    evidence_dir.mkdir(mode=0o700, parents=True, exist_ok=True)
    evidence_dir.chmod(0o700)
    metadata = evidence_dir.stat()
    if metadata.st_uid != os.geteuid() or stat.S_IMODE(metadata.st_mode) & 0o077:
        raise VerificationError("evidence directory must be root-private")
    destination = evidence_dir / (time.strftime("%Y%m%dT%H%M%SZ", time.gmtime()) + "-" + secrets.token_hex(4))
    os.replace(work, destination)
    return destination


def parse_args(argv: list[str]) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--subscription-url-file", type=Path, required=True, help="mode-0600 private Clash URL file")
    parser.add_argument("--mihomo", type=Path, required=True, help="absolute path to the verified Mihomo binary")
    parser.add_argument("--home", type=Path, required=True, help="existing private parent directory for temporary Mihomo files")
    parser.add_argument("--evidence-dir", type=Path, default=DEFAULT_EVIDENCE_DIR, help=f"root-private failed-run evidence directory (default: {DEFAULT_EVIDENCE_DIR})")
    parser.add_argument("--test-url", default=DEFAULT_TEST_URL, help="strict HTTPS 204 endpoint used by Mihomo delay API")
    parser.add_argument("--dry-run", action="store_true", help="validate local arguments and print expected protocol/family rows without starting Mihomo")
    return parser.parse_args(argv)


def main(argv: list[str]) -> int:
    args = parse_args(argv)
    process: subprocess.Popen[str] | None = None
    work: Path | None = None
    retain_failed_evidence = False
    try:
        if os.name != "posix" or os.geteuid() != 0:
            raise VerificationError("run this verifier as root on Linux")
        if not args.mihomo.is_absolute() or not args.mihomo.is_file() or not os.access(args.mihomo, os.X_OK):
            raise VerificationError("--mihomo must be an executable absolute path")
        if not args.home.is_dir():
            raise VerificationError("--home must be an existing directory")
        parsed_test = urlparse(args.test_url)
        if parsed_test.scheme != "https" or not parsed_test.netloc:
            raise VerificationError("--test-url must be HTTPS; certificate verification is never disabled")
        if args.dry_run:
            for _, (protocol, family) in EXPECTED_NODES.items():
                print_result(protocol, family, "-", True)
            return 0

        subscription_url = read_private_url(args.subscription_url_file)
        require_mihomo_flags(args.mihomo)
        work = Path(tempfile.mkdtemp(prefix="zenith-mihomo-verify-", dir=args.home))
        work.chmod(0o700)
        controller_port, mixed_port = free_loopback_port(), free_loopback_port()
        while mixed_port == controller_port:
            mixed_port = free_loopback_port()
        controller_secret = secrets.token_urlsafe(32)
        config_path = work / "config.yaml"
        log_path = work / "mihomo.log"
        write_config(config_path, subscription_url, controller_port, mixed_port, controller_secret)
        with open(log_path, "w", encoding="utf-8") as log:
            process = subprocess.Popen(
                [
                    str(args.mihomo), "-d", str(work), "-f", str(config_path),
                    "-ext-ctl", f"127.0.0.1:{controller_port}", "-secret", controller_secret,
                ],
                stdout=log,
                stderr=subprocess.STDOUT,
                text=True,
            )
        wait_for_controller(controller_port, controller_secret, process)
        provider = provider_proxies(controller_port, controller_secret, work / "zenith-private-provider.yaml")
        passed = validate_nodes(provider, controller_port, controller_secret, args.test_url)
        retain_failed_evidence = not passed
        return 0 if passed else 2
    except VerificationError as exc:
        # Deliberately omit command output and exception detail from Mihomo;
        # either might include the provider URL or a node credential.
        print(f"verification stopped: {exc}", file=sys.stderr)
        retain_failed_evidence = True
        return 2
    finally:
        stop_process(process)
        if work is not None:
            if retain_failed_evidence:
                try:
                    retained = retain_evidence(work, args.evidence_dir)
                    print(f"private evidence retained: {retained}", file=sys.stderr)
                except (OSError, VerificationError):
                    print("private evidence retention failed", file=sys.stderr)
                    shutil.rmtree(work, ignore_errors=True)
            else:
                shutil.rmtree(work, ignore_errors=True)


if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1:]))
