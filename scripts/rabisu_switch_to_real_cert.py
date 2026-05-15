#!/usr/bin/env python3
"""Switches rabisu's 5 TLS-using inbounds from the 7-day self-signed test
cert to the real Let's Encrypt cert just issued for 136-175-83-32.nip.io.

  - certificateFile/keyFile → the new ACME-managed paths
  - serverName → 136-175-83-32.nip.io (so SNI matches the cert CN)
  - allowInsecure → removed (clients now verify the chain properly)

After: proxy apply so both engines pick up the new cert paths.
"""
import json
import subprocess

CERT = "/opt/zenithpanel/data/certs/136-175-83-32.nip.io.crt"
KEY = "/opt/zenithpanel/data/certs/136-175-83-32.nip.io.key"
SERVER_NAME = "136-175-83-32.nip.io"

# Only inbounds that actually do TLS; ss-2022 (id 5) and vless-reality (id 1)
# don't carry a tlsSettings block.
TLS_IDS = [2, 3, 4, 7]


def main():
    listing = json.loads(subprocess.check_output(["zenithctl", "-q", "raw", "GET", "/api/v1/inbounds"]))
    by_id = {row["id"]: row for row in listing}
    for inbound_id in TLS_IDS:
        ib = by_id.get(inbound_id)
        if ib is None:
            print(f"[skip] inbound {inbound_id} missing")
            continue
        stream = json.loads(ib.get("stream") or "{}")
        tls = stream.setdefault("tlsSettings", {})
        tls["serverName"] = SERVER_NAME
        tls["certificates"] = [{"certificateFile": CERT, "keyFile": KEY}]
        # Real cert means we no longer need to ask clients to skip verify.
        tls.pop("allowInsecure", None)
        ib["stream"] = json.dumps(stream)
        body = json.dumps(ib)
        path = f"/tmp/inbound_{inbound_id}.json"
        with open(path, "w") as f:
            f.write(body)
        subprocess.check_call(["zenithctl", "raw", "PUT", f"/api/v1/inbounds/{inbound_id}", "--data", "@" + path])
        print(f"[ok] inbound {inbound_id} {ib['tag']}: TLS pointed at real cert; allowInsecure removed")

    # Reload both engines so the new cert path is picked up. Sing-box reads
    # cert files at start, so this is essential for hy2/tuic.
    subprocess.check_call(["zenithctl", "proxy", "apply"])


if __name__ == "__main__":
    main()
