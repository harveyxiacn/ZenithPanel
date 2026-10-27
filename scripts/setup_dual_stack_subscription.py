#!/usr/bin/env python3
"""Provision a private, dual-stack Clash subscription through ZenithPanel's API.

Run this *on the panel host as root* after a valid certificate has been issued
by ZenithPanel.  It deliberately talks only to the panel's trusted Unix-socket
API via ``docker exec``; it never writes the SQLite database directly.

The state directory is intentionally root-private.  It contains the shared
client UUID, Reality private key, obfuscation password, API transcripts and the
subscription URL.  Do not put it in a home directory shared with other users
or commit it to a repository.

This helper creates only ``clash-dual-*`` resources.  A same-named resource
which is not recorded in its state file is treated as a collision and stops the
run rather than risking an existing user's node.  It does not remove anything
on failure: re-run it after addressing the reported condition.
"""

from __future__ import annotations

import argparse
import copy
import ipaddress
import json
import os
from pathlib import Path
import re
import secrets
import stat
import subprocess
import sys
import time
import uuid
from typing import Any, Callable


PREFIX = "clash-dual-"
STATE_NAME = "state.json"
LOG_NAME = "api-transcript.log"
LINK_NAME = "clash-subscription.url"
SOCKET_HOST = "unix:///run/zenithpanel.sock"
PANEL_BINARY = "/opt/zenithpanel/zenithpanel"
DEFAULT_STATE_DIR = Path("/root/zenithpanel-clash-dual")
TAG_ORDER = (
    "clash-dual-vless-reality",
    "clash-dual-vmess-ws",
    "clash-dual-trojan",
    "clash-dual-shadowsocks",
    "clash-dual-hysteria2",
    "clash-dual-tuic",
)
TAG_PROTOCOL = {
    "clash-dual-vless-reality": "vless",
    "clash-dual-vmess-ws": "vmess",
    "clash-dual-trojan": "trojan",
    "clash-dual-shadowsocks": "shadowsocks",
    "clash-dual-hysteria2": "hysteria2",
    "clash-dual-tuic": "tuic",
}


class ProvisioningError(RuntimeError):
    """An expected, operator-actionable provisioning stop."""


def _run_ufw_status() -> tuple[int, str]:
    """Read UFW's status without changing host firewall state."""
    environment = os.environ.copy()
    environment["LC_ALL"] = "C"
    try:
        completed = subprocess.run(
            ["ufw", "status"],
            capture_output=True,
            text=True,
            env=environment,
            timeout=5,
            check=False,
        )
    except (OSError, subprocess.SubprocessError):
        return 127, ""
    return completed.returncode, completed.stdout


# Test code replaces this seam with deterministic command output. Keeping the
# fallback read-only and optional means a host without UFW remains advisory.
ufw_status_runner: Callable[[], tuple[int, str]] = _run_ufw_status


def ufw_public_accepts() -> dict[str, set[tuple[str, str]]]:
    """Return exact all-source UFW accepts, split by address family.

    ``ufw status`` is parsed with a fixed C locale. The source column must be
    exactly ``Anywhere`` for IPv4 or ``Anywhere (v6)`` for IPv6, and the
    destination must include the requested protocol and (for IPv6) the v6
    marker. Interface- or source-restricted rules therefore never count as a
    public firewall accept.
    """
    status, output = ufw_status_runner()
    if status != 0 or not re.search(r"(?m)^Status:\s*active\s*$", output):
        return {"ipv4": set(), "ipv6": set()}
    accepts: dict[str, set[tuple[str, str]]] = {"ipv4": set(), "ipv6": set()}
    for line in output.splitlines():
        match = re.match(
            r"^\s*(?P<port>[0-9]+/(?P<protocol>tcp|udp))(?P<v6>\s+\(v6\))?\s+ALLOW\s+(?P<source>.+?)\s*$",
            line,
            re.IGNORECASE,
        )
        if not match:
            continue
        ipv6 = bool(match.group("v6"))
        family = "ipv6" if ipv6 else "ipv4"
        expected_source = "Anywhere (v6)" if ipv6 else "Anywhere"
        # UFW appends an optional human comment after the source column. Strip
        # only that comment suffix; source and interface restrictions remain
        # part of the value being compared and therefore never match.
        source = re.sub(r"\s+#.*$", "", match.group("source"))
        if source == expected_source:
            accepts[family].add((match.group("protocol").lower(), match.group("port").split("/", 1)[0]))
    return accepts


def compact_json(value: Any) -> str:
    return json.dumps(value, separators=(",", ":"), sort_keys=True)


def normalized_json(value: Any) -> Any:
    if isinstance(value, str):
        try:
            return json.loads(value)
        except json.JSONDecodeError:
            return value
    return value


def private_write(path: Path, text: str) -> None:
    path.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    try:
        path.parent.chmod(0o700)
    except OSError:
        pass
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    with os.fdopen(fd, "w", encoding="utf-8") as handle:
        handle.write(text)
    try:
        path.chmod(0o600)
    except OSError:
        pass


def assert_private_directory(path: Path) -> None:
    path.mkdir(mode=0o700, parents=True, exist_ok=True)
    mode = stat.S_IMODE(path.stat().st_mode)
    if mode & 0o077:
        raise ProvisioningError(f"state directory {path} must not be group/world accessible")
    if path.stat().st_uid != os.geteuid():
        raise ProvisioningError(f"state directory {path} is not owned by the current user")


class PanelAPI:
    def __init__(self, container: str, state_dir: Path) -> None:
        self.container = container
        self.log_path = state_dir / LOG_NAME

    def call(self, method: str, path: str, payload: Any | None = None) -> Any:
        command = [
            "docker", "exec", "-i", self.container, PANEL_BINARY, "ctl",
            "--host", SOCKET_HOST, "--timeout", "120s", "-q", "raw", method.upper(), path,
        ]
        stdin: str | None = None
        if payload is not None:
            command.extend(["--data", "-"])
            stdin = compact_json(payload)
        try:
            completed = subprocess.run(command, input=stdin, text=True, capture_output=True, timeout=180)
        except subprocess.TimeoutExpired as exc:
            private_write(
                self.log_path,
                (self.log_path.read_text(encoding="utf-8") if self.log_path.exists() else "")
                + f"\n--- {method.upper()} {path} timed out after {exc.timeout}s ---\n",
            )
            raise ProvisioningError(f"panel API {method.upper()} {path} timed out; inspect private {self.log_path}") from exc
        transcript = (
            f"\n--- {time.strftime('%Y-%m-%dT%H:%M:%SZ', time.gmtime())} {method.upper()} {path} "
            f"exit={completed.returncode} ---\nstdout:\n{completed.stdout}\nstderr:\n{completed.stderr}\n"
        )
        with open(self.log_path, "a", encoding="utf-8") as handle:
            handle.write(transcript)
        try:
            self.log_path.chmod(0o600)
        except OSError:
            pass
        if completed.returncode != 0:
            raise ProvisioningError(f"panel API {method.upper()} {path} failed; inspect private {self.log_path}")
        try:
            return json.loads(completed.stdout)
        except json.JSONDecodeError as exc:
            raise ProvisioningError(
                f"panel API {method.upper()} {path} returned invalid JSON; inspect private {self.log_path}"
            ) from exc

    def readable_file(self, path: str) -> bool:
        # Positional parameters avoid interpolating an operator-supplied path
        # into a shell command.
        completed = subprocess.run(
            ["docker", "exec", self.container, "sh", "-c", 'test -r "$1"', "sh", path],
            text=True,
            capture_output=True,
        )
        return completed.returncode == 0


def load_state(path: Path) -> dict[str, Any]:
    if not path.exists():
        return {}
    mode = stat.S_IMODE(path.stat().st_mode)
    if mode & 0o077:
        raise ProvisioningError(f"state file {path} must not be group/world accessible")
    if path.stat().st_uid != os.geteuid():
        raise ProvisioningError(f"state file {path} is not owned by the current user")
    try:
        loaded = json.loads(path.read_text(encoding="utf-8"))
    except json.JSONDecodeError as exc:
        raise ProvisioningError(f"state file {path} is invalid JSON; restore it from a private backup") from exc
    if not isinstance(loaded, dict):
        raise ProvisioningError(f"state file {path} has an invalid root object")
    return loaded


def save_state(path: Path, state: dict[str, Any]) -> None:
    # A state write can contain the only persisted record of the UUID/Reality
    # key ownership.  Never truncate it in place: a crash must leave either the
    # old complete document or the new complete document.
    temp = path.with_name(f".{path.name}.{os.getpid()}.{secrets.token_hex(6)}.tmp")
    try:
        private_write(temp, json.dumps(state, indent=2, sort_keys=True) + "\n")
        with open(temp, "r+", encoding="utf-8") as handle:
            handle.flush()
            os.fsync(handle.fileno())
        os.replace(temp, path)
        try:
            path.chmod(0o600)
        except OSError:
            pass
    finally:
        try:
            temp.unlink()
        except FileNotFoundError:
            pass


def require(value: str, name: str) -> str:
    value = value.strip()
    if not value:
        raise ProvisioningError(f"{name} is required")
    return value


def validate_inputs(args: argparse.Namespace) -> None:
    try:
        ipaddress.IPv4Address(args.ipv4)
        ipaddress.IPv6Address(args.ipv6)
    except ipaddress.AddressValueError as exc:
        raise ProvisioningError("--ipv4 and --ipv6 must be literal public IP addresses") from exc
    if not re.fullmatch(r"(?=.{1,253}$)(?:[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?\.)+[A-Za-z]{2,63}", args.cert_domain):
        raise ProvisioningError("--cert-domain must be a DNS name accepted by ZenithPanel ACME")
    for name in ("cert_path", "key_path"):
        value = getattr(args, name)
        if not value.startswith("/opt/zenithpanel/data/certs/"):
            raise ProvisioningError(f"--{name.replace('_', '-')} must be under /opt/zenithpanel/data/certs/")
    expected_cert = f"/opt/zenithpanel/data/certs/{args.cert_domain}.crt"
    expected_key = f"/opt/zenithpanel/data/certs/{args.cert_domain}.key"
    if args.cert_path != expected_cert or args.key_path != expected_key:
        raise ProvisioningError(
            "the HTTPS subscription listener accepts ZenithPanel ACME paths only; "
            f"use --cert-path {expected_cert} --key-path {expected_key}"
        )
    if not re.fullmatch(r"[A-Za-z0-9.-]+", args.subscription_public_host):
        raise ProvisioningError("--subscription-public-host must be a DNS name or IPv4 literal")
    if not re.fullmatch(r"[A-Za-z0-9_.-]{1,48}", args.container):
        raise ProvisioningError("--container contains unsupported characters")


def initial_state(args: argparse.Namespace) -> dict[str, Any]:
    return {
        "version": 1,
        "prefix": PREFIX,
        "shared_uuid": str(uuid.uuid4()),
        "created_at": int(time.time()),
        "inputs": {
            "ipv4": args.ipv4,
            "ipv6": args.ipv6,
            "cert_domain": args.cert_domain,
            "cert_path": args.cert_path,
            "key_path": args.key_path,
            "subscription_public_host": args.subscription_public_host,
            "subscription_port": args.subscription_port,
        },
        "inbounds": {},
        "subscription": {},
    }


def ensure_state_matches_inputs(state: dict[str, Any], args: argparse.Namespace) -> None:
    if state.get("version") != 1 or state.get("prefix") != PREFIX:
        raise ProvisioningError("state file belongs to a different provisioning format")
    if not isinstance(state.get("shared_uuid"), str) or not state["shared_uuid"]:
        raise ProvisioningError("state file is missing the shared UUID")
    expected = initial_state(args)["inputs"]
    if state.get("inputs") != expected:
        raise ProvisioningError("arguments do not match the existing private state; use the original values")
    if not isinstance(state.get("inbounds"), dict):
        raise ProvisioningError("state file has invalid inbound state")


def inbound_payloads(state: dict[str, Any]) -> dict[str, dict[str, Any]]:
    values = state["inputs"]
    shared_uuid = state["shared_uuid"]
    tls = {
        "network": "tcp",
        "security": "tls",
        "tlsSettings": {
            "serverName": values["cert_domain"],
            "certificates": [{"certificateFile": values["cert_path"], "keyFile": values["key_path"]}],
        },
    }
    common = {
        "listen": "::",
        "server_address": values["ipv4"],
        "server_address_ipv4": values["ipv4"],
        "server_address_ipv6": values["ipv6"],
        "enable": True,
    }

    reality = state.get("reality")
    if not isinstance(reality, dict):
        raise ProvisioningError("Reality keys have not been generated")
    hy2_obfs = state.get("hysteria2_obfs_password")
    if not isinstance(hy2_obfs, str) or not hy2_obfs:
        raise ProvisioningError("Hysteria2 obfuscation password has not been generated")

    payloads: dict[str, dict[str, Any]] = {}
    payloads["clash-dual-vless-reality"] = {
        **common,
        "tag": "clash-dual-vless-reality", "protocol": "vless", "port": 443, "network": "tcp",
        "settings": compact_json({"decryption": "none", "flow": "xtls-rprx-vision"}),
        "stream": compact_json({
            "network": "tcp", "security": "reality",
            "realitySettings": {
                "show": False, "target": "www.apple.com:443", "serverNames": ["www.apple.com"],
                "privateKey": reality["private_key"], "shortIds": [reality["short_id"]],
                "settings": {"publicKey": reality["public_key"], "fingerprint": "chrome"},
            },
        }),
    }
    vmess_stream = copy.deepcopy(tls)
    vmess_stream["network"] = "ws"
    vmess_stream["wsSettings"] = {"path": "/clash-dual-vmess", "headers": {"Host": values["cert_domain"]}}
    payloads["clash-dual-vmess-ws"] = {
        **common,
        "tag": "clash-dual-vmess-ws", "protocol": "vmess", "port": 31402, "network": "ws",
        "settings": compact_json({}), "stream": compact_json(vmess_stream),
    }
    payloads["clash-dual-trojan"] = {
        **common,
        "tag": "clash-dual-trojan", "protocol": "trojan", "port": 31403, "network": "tcp",
        "settings": compact_json({}), "stream": compact_json(tls),
    }
    # Classic Shadowsocks has one server password.  Setting it to the shared
    # UUID makes the URI credential and every other protocol's credential the
    # same; the companion Client row only makes it part of the unified sub.
    payloads["clash-dual-shadowsocks"] = {
        **common,
        "tag": "clash-dual-shadowsocks", "protocol": "shadowsocks", "port": 31404, "network": "tcp",
        "settings": compact_json({"method": "chacha20-ietf-poly1305", "password": shared_uuid}),
        "stream": compact_json({"network": "tcp"}),
    }
    quic_stream = copy.deepcopy(tls)
    quic_stream["network"] = "udp"
    quic_stream["tlsSettings"]["alpn"] = ["h3"]
    payloads["clash-dual-hysteria2"] = {
        **common,
        "tag": "clash-dual-hysteria2", "protocol": "hysteria2", "port": 8443, "network": "udp",
        "settings": compact_json({
            "obfs": {"type": "salamander", "password": hy2_obfs}, "up_mbps": 100, "down_mbps": 100,
        }), "stream": compact_json(quic_stream),
    }
    payloads["clash-dual-tuic"] = {
        **common,
        "tag": "clash-dual-tuic", "protocol": "tuic", "port": 31406, "network": "udp",
        "settings": compact_json({"congestion_control": "bbr"}), "stream": compact_json(quic_stream),
    }
    return payloads


def payload_matches(actual: dict[str, Any], expected: dict[str, Any]) -> bool:
    fields = (
        "tag", "protocol", "listen", "server_address", "server_address_ipv4", "server_address_ipv6",
        "port", "network", "enable",
    )
    if any(actual.get(field) != expected.get(field) for field in fields):
        return False
    return (
        normalized_json(actual.get("settings")) == normalized_json(expected["settings"])
        and normalized_json(actual.get("stream")) == normalized_json(expected["stream"])
    )


def client_plan(tag: str, inbound_id: int, shared_uuid: str) -> dict[str, Any]:
    return {
        "inbound_id": inbound_id,
        "email": f"{tag}-shared",
        "uuid": shared_uuid,
        "enable": True,
        "total": 0,
        "expiry_time": 0,
    }


def find_matching_client(clients: list[dict[str, Any]], planned: dict[str, Any]) -> dict[str, Any] | None:
    matches = [
        client for client in clients
        if client.get("inbound_id") == planned["inbound_id"] and client.get("email") == planned["email"]
    ]
    if not matches:
        return None
    if len(matches) != 1 or matches[0].get("uuid") != planned["uuid"]:
        raise ProvisioningError(f"client collision for {planned['email']}; refusing to alter it")
    return matches[0]


def preflight(api: PanelAPI, args: argparse.Namespace, state: dict[str, Any] | None) -> list[str]:
    """Return missing explicit firewall accepts without changing the host.

    The firewall API exposes rules but not the chain's default policy, so this
    is intentionally advisory: an operator may know that a provider firewall
    or an accept-by-default chain makes an explicit local rule unnecessary.
    It never adds a rule itself.
    """
    inbounds = api.call("GET", "/api/v1/inbounds")
    if not isinstance(inbounds, list):
        raise ProvisioningError("inbound list has an unexpected response; inspect private transcript")
    owned = state.get("inbounds", {}) if state else {}
    wanted_ports = {443, 31402, 31403, 31404, 8443, 31406}
    for inbound in inbounds:
        if not isinstance(inbound, dict):
            continue
        tag = inbound.get("tag")
        if isinstance(tag, str) and tag.startswith(PREFIX) and tag not in owned:
            raise ProvisioningError(f"inbound tag collision at {tag}; it is not owned by this state file")
        if inbound.get("enable") is True and inbound.get("port") in wanted_ports and tag not in owned:
            raise ProvisioningError(
                f"enabled inbound {tag!r} already uses requested port {inbound.get('port')}; refusing to overlap it"
            )

    needed = {
        ("tcp", "443"), ("tcp", "31402"), ("tcp", "31403"), ("tcp", "31404"),
        ("udp", "31404"), ("udp", "8443"), ("udp", "31406"),
        ("tcp", str(args.subscription_port)),
    }
    missing: list[str] = []
    ufw_accepts = {"ipv4": set(), "ipv6": set()}
    ufw_checked = False
    for family in ("ipv4", "ipv6"):
        rules = api.call("GET", f"/api/v1/firewall/rules?family={family}")
        if not isinstance(rules, list):
            raise ProvisioningError(f"{family} firewall list has an unexpected response; inspect private transcript")
        accepts = {
            (str(rule.get("protocol", "")).lower(), str(rule.get("port", "")))
            for rule in rules if isinstance(rule, dict) and str(rule.get("target", "")).upper() == "ACCEPT"
        }
        for proto, port in sorted(needed):
            if (proto, port) in accepts:
                continue
            if not ufw_checked:
                ufw_accepts = ufw_public_accepts()
                ufw_checked = True
            if (proto, port) not in ufw_accepts[family]:
                missing.append(f"{family} {proto}/{port}")

    # Reject a different active listener before creating any inbounds.  The
    # configuration is read again immediately before a PUT to cover a race
    # with another administrator changing it after this preflight.
    subscription = api.call("GET", "/api/v1/admin/subscription")
    if not isinstance(subscription, dict):
        raise ProvisioningError("subscription server status is invalid; inspect private transcript")
    expected_subscription = {
        "port": args.subscription_port,
        "public_host": args.subscription_public_host,
        "cert_domain": args.cert_domain,
    }
    if subscription.get("enabled") is True and any(
        subscription.get(key) != value for key, value in expected_subscription.items()
    ):
        raise ProvisioningError(
            "an enabled subscription server has different port/public host/certificate settings; refusing to override it"
        )
    return missing


def ensure_inbounds_and_clients(api: PanelAPI, state_path: Path, state: dict[str, Any]) -> None:
    listed = api.call("GET", "/api/v1/inbounds")
    if not isinstance(listed, list):
        raise ProvisioningError("inbound list has an unexpected response; inspect private transcript")
    by_tag = {item.get("tag"): item for item in listed if isinstance(item, dict)}

    # Generate sensitive material once and persist it before any inbound API
    # mutation.  The generated key response remains only in the private state
    # file and transcript.
    if "reality" not in state:
        reality = api.call("POST", "/api/v1/proxy/generate-reality-keys")
        if not isinstance(reality, dict) or not all(isinstance(reality.get(k), str) and reality[k] for k in ("private_key", "public_key", "short_id")):
            raise ProvisioningError("Reality key API returned an unexpected response; inspect private transcript")
        state["reality"] = {key: reality[key] for key in ("private_key", "public_key", "short_id")}
        state["hysteria2_obfs_password"] = secrets.token_urlsafe(24)
        save_state(state_path, state)

    payloads = inbound_payloads(state)
    for tag in TAG_ORDER:
        expected = payloads[tag]
        recorded = state["inbounds"].get(tag)
        actual = by_tag.get(tag)
        if actual is not None:
            if not isinstance(recorded, dict):
                raise ProvisioningError(f"inbound tag collision at {tag}; it is not owned by this state file")
            planned = recorded.get("payload")
            if not isinstance(planned, dict) or not payload_matches(actual, planned):
                raise ProvisioningError(f"existing {tag} differs from private state; refusing to change it")
            if recorded.get("id") not in (None, actual.get("id")):
                raise ProvisioningError(f"existing {tag} has a different ID than private state")
            recorded["id"] = actual.get("id")
            save_state(state_path, state)
        else:
            if recorded is not None and recorded.get("id"):
                raise ProvisioningError(f"private state says {tag} exists but it was not found; refusing recreation")
            if recorded is None:
                state["inbounds"][tag] = {"payload": expected, "id": None, "client": None}
                recorded = state["inbounds"][tag]
                save_state(state_path, state)
            created = api.call("POST", "/api/v1/inbounds", recorded["payload"])
            if not isinstance(created, dict) or not isinstance(created.get("id"), int):
                raise ProvisioningError(f"create {tag} returned an unexpected response; inspect private transcript")
            if not payload_matches(created, recorded["payload"]):
                raise ProvisioningError(f"create {tag} returned a mismatched inbound; refusing to continue")
            recorded["id"] = created["id"]
            by_tag[tag] = created
            save_state(state_path, state)

        inbound_id = state["inbounds"][tag]["id"]
        if not isinstance(inbound_id, int) or inbound_id < 1:
            raise ProvisioningError(f"private state has no valid ID for {tag}")
        planned_client = client_plan(tag, inbound_id, state["shared_uuid"])
        recorded_client = state["inbounds"][tag].get("client")
        clients = api.call("GET", f"/api/v1/clients?inbound_id={inbound_id}")
        if not isinstance(clients, list):
            raise ProvisioningError("client list has an unexpected response; inspect private transcript")
        existing_client = find_matching_client(clients, planned_client)
        if existing_client is not None:
            if isinstance(recorded_client, dict) and recorded_client.get("id") not in (None, existing_client.get("id")):
                raise ProvisioningError(f"client for {tag} has a different ID than private state")
            state["inbounds"][tag]["client"] = {"id": existing_client.get("id"), "plan": planned_client}
            save_state(state_path, state)
            continue
        if isinstance(recorded_client, dict) and recorded_client.get("id"):
            raise ProvisioningError(f"private state says client for {tag} exists but it was not found; refusing recreation")
        state["inbounds"][tag]["client"] = {"id": None, "plan": planned_client}
        save_state(state_path, state)
        created_client = api.call("POST", "/api/v1/clients", planned_client)
        if not isinstance(created_client, dict) or not isinstance(created_client.get("id"), int):
            raise ProvisioningError(f"create client for {tag} returned an unexpected response; inspect private transcript")
        if created_client.get("uuid") != state["shared_uuid"] or created_client.get("inbound_id") != inbound_id:
            raise ProvisioningError(f"create client for {tag} returned a mismatched credential; refusing to continue")
        state["inbounds"][tag]["client"] = {"id": created_client["id"], "plan": planned_client}
        save_state(state_path, state)


def apply_and_verify(api: PanelAPI, state_path: Path, state: dict[str, Any]) -> None:
    api.call("POST", "/api/v1/proxy/apply")
    checks: list[dict[str, Any]] = []
    for tag in TAG_ORDER:
        inbound_id = state["inbounds"][tag]["id"]
        for family in ("ipv4", "ipv6"):
            result = api.call("GET", f"/api/v1/proxy/test/{inbound_id}?family={family}")
            if not isinstance(result, dict):
                raise ProvisioningError(f"{tag} {family} probe returned an unexpected response")
            checks.append({"tag": tag, "family": family, "ok": result.get("ok"), "e2e": result.get("e2e")})
            if result.get("ok") is not True or result.get("e2e") != "passed":
                state["last_verification"] = checks
                save_state(state_path, state)
                raise ProvisioningError(
                    f"{tag} {family} did not pass its end-to-end probe; inspect private {api.log_path}"
                )
    state["last_verification"] = checks
    state["verified_at"] = int(time.time())
    save_state(state_path, state)


def configure_subscription_server(api: PanelAPI, state_path: Path, state: dict[str, Any]) -> Path:
    values = state["inputs"]
    existing = api.call("GET", "/api/v1/admin/subscription")
    if not isinstance(existing, dict):
        raise ProvisioningError("subscription server status is invalid; inspect private transcript")
    expected = {
        "port": values["subscription_port"],
        "public_host": values["subscription_public_host"],
        "cert_domain": values["cert_domain"],
    }
    if existing.get("enabled") is True:
        if any(existing.get(key) != value for key, value in expected.items()):
            raise ProvisioningError(
                "an enabled subscription server has different port/public host/certificate settings; refusing to override it"
            )
        status = existing
    else:
        response = api.call("PUT", "/api/v1/admin/subscription", {
            "enabled": True,
            **expected,
            "regenerate_secret": False,
        })
        if not isinstance(response, dict):
            raise ProvisioningError("subscription server response is invalid; inspect private transcript")
        status = response.get("status")
    if not isinstance(status, dict) or status.get("running") is not True or status.get("tls") is not True:
        raise ProvisioningError("subscription server is not running with TLS; inspect private transcript")
    base_url = status.get("base_url")
    if not isinstance(base_url, str) or not base_url.startswith("https://") or not base_url.endswith("/"):
        raise ProvisioningError("subscription server did not provide a secure base URL; inspect private transcript")
    link_path = state_path.parent / LINK_NAME
    private_write(link_path, base_url + state["shared_uuid"] + "?format=clash&dual_stack=1\n")
    state["subscription"] = {
        "configured_at": int(time.time()),
        "port": values["subscription_port"],
        "public_host": values["subscription_public_host"],
        "link_file": str(link_path),
    }
    save_state(state_path, state)
    return link_path


def parse_args(argv: list[str]) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--ipv4", required=True, help="public IPv4 literal stored in every inbound")
    parser.add_argument("--ipv6", required=True, help="public IPv6 literal stored in every inbound")
    parser.add_argument("--cert-domain", required=True, help="DNS name covered by the panel-issued certificate")
    parser.add_argument("--cert-path", required=True, help="panel ACME certificate path for --cert-domain")
    parser.add_argument("--key-path", required=True, help="panel ACME key path for --cert-domain")
    parser.add_argument("--subscription-public-host", required=True, help="DNS host placed in the HTTPS subscription URL")
    parser.add_argument("--subscription-port", type=int, default=2096, help="HTTPS subscription listener port (default: 2096)")
    parser.add_argument("--container", default="zenithpanel", help="panel container name (default: zenithpanel)")
    parser.add_argument("--state-dir", type=Path, default=DEFAULT_STATE_DIR, help=f"root-private state directory (default: {DEFAULT_STATE_DIR})")
    parser.add_argument("--dry-run", action="store_true", help="validate arguments and show the six sanitized resources without contacting Docker")
    parser.add_argument("--preflight-only", action="store_true", help="read inbound/firewall state without creating or changing anything")
    return parser.parse_args(argv)


def main(argv: list[str]) -> int:
    args = parse_args(argv)
    try:
        if not 1 <= args.subscription_port <= 65535:
            raise ProvisioningError("--subscription-port must be 1-65535")
        validate_inputs(args)
        if args.dry_run:
            print("dry run: would provision " + ", ".join(TAG_ORDER))
            print("ports: 443/tcp, 31402/tcp, 31403/tcp, 31404/tcp, 8443/udp, 31406/udp")
            print("each inbound will listen on :: and store explicit IPv4/IPv6 endpoints")
            return 0
        if os.geteuid() != 0:
            raise ProvisioningError("run as root so the state directory and Docker access stay private")
        assert_private_directory(args.state_dir)
        state_path = args.state_dir / STATE_NAME
        state = load_state(state_path)
        if state:
            ensure_state_matches_inputs(state, args)
        api = PanelAPI(args.container, args.state_dir)
        if args.preflight_only:
            missing = preflight(api, args, state or None)
            if missing:
                print("preflight: no resources changed; explicit firewall accepts not found:")
                for item in missing:
                    print("  " + item)
                print("Review host/provider firewall policy before the non-preflight run.")
            else:
                print("preflight: no tag/port collision and all requested explicit accepts are present")
            return 0
        missing = preflight(api, args, state or None)
        if missing:
            raise ProvisioningError(
                "firewall preflight is missing explicit accepts: " + ", ".join(missing)
                + "; run --preflight-only and have the operator review/open them before provisioning"
            )
        if not state:
            state = initial_state(args)
            # The fresh UUID exists before any remote mutation.
            save_state(state_path, state)
        if not api.readable_file(args.cert_path) or not api.readable_file(args.key_path):
            raise ProvisioningError("certificate or key is not readable in the panel container")
        ensure_inbounds_and_clients(api, state_path, state)
        apply_and_verify(api, state_path, state)
        link_path = configure_subscription_server(api, state_path, state)
        print("provisioning completed: 6 clash-dual inbounds verified across IPv4 and IPv6")
        print(f"private state and API transcript: {args.state_dir}")
        print(f"private Clash subscription URL: {link_path}")
        print("no existing inbounds or clients were modified")
        return 0
    except ProvisioningError as exc:
        print(f"provisioning stopped: {exc}", file=sys.stderr)
        return 2


if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1:]))
