#!/usr/bin/env python3
"""Patches the 4 test inbounds on rabisu so they're reachable from a real
client scanning a QR. Run from the panel host via:

    python3 /root/proto-tests/fix_inbounds.py

The script is idempotent: re-running with the same target values is a no-op.
"""
import json
import os
import subprocess
import sys

TARGET_HOST = "136.175.83.32"
TARGETS = {
    3: {"insecure": True},   # vmess+ws+tls
    4: {"insecure": True},   # trojan+tls
    5: {"insecure": False},  # ss-2022 (no TLS)
    7: {"insecure": True},   # tuic v5
}


def ctl(*args, capture=True):
    """Invoke zenithctl with the given args. capture=True returns parsed JSON
    from stdout; capture=False just runs and lets stdout/stderr flow through.
    """
    cmd = ["zenithctl", *args]
    if capture:
        out = subprocess.check_output(cmd, text=True)
        return json.loads(out)
    subprocess.check_call(cmd)


def main():
    listing = ctl("-q", "raw", "GET", "/api/v1/inbounds")
    by_id = {row["id"]: row for row in listing}
    for inbound_id, opts in TARGETS.items():
        ib = by_id.get(inbound_id)
        if ib is None:
            print(f"[skip] inbound {inbound_id} not present")
            continue

        before = ib.get("server_address", "")
        ib["server_address"] = TARGET_HOST

        # stream is stored as a JSON string; mutate it as a dict, then
        # re-serialize. allowInsecure is the Xray flag that tells generated
        # subscriptions to set skip-verify on the client side. For ss-2022
        # we don't touch the stream.
        if opts["insecure"]:
            stream = json.loads(ib.get("stream", "{}") or "{}")
            settings = stream.setdefault("tlsSettings", {})
            settings["allowInsecure"] = True
            ib["stream"] = json.dumps(stream)

        body = json.dumps(ib)
        # zenithctl raw PUT consumes JSON from --data.
        with open(f"/tmp/inbound_{inbound_id}.json", "w") as f:
            f.write(body)
        subprocess.check_call([
            "zenithctl", "raw", "PUT",
            f"/api/v1/inbounds/{inbound_id}",
            "--data", f"@/tmp/inbound_{inbound_id}.json",
        ])
        os.remove(f"/tmp/inbound_{inbound_id}.json")
        print(f"[ok] inbound {inbound_id} {ib['tag']}: server_address {before} -> {TARGET_HOST}"
              + (" + allowInsecure=true" if opts["insecure"] else ""))

    # Re-apply so the engines pick up the new server_address (subscription URL
    # uses the DB row directly; engines don't need it, but apply keeps caches
    # warm).
    subprocess.check_call(["zenithctl", "proxy", "apply"])


if __name__ == "__main__":
    main()
