# One Clash Verge subscription for all protocols and address families

Use the Mihomo core in Clash Verge. The opt-in subscription URL is:

```text
https://<subscription-host>:<port>/<private-path>/<client-uuid>?format=clash&dual_stack=1
```

The panel also serves the same format at `/api/v1/sub/<client-uuid>`. The
subscription URL is a credential; share it only with the intended user.

In **Proxy → Users & Subs → Subscription Link**, select **Clash / Mihomo**,
enable **Dual-stack automatic subscription**, and copy the URL. Import it in
Clash Verge's subscription screen. In the `PROXY` group, choose:

- `AUTO` (default): measure all nodes every 60 seconds and choose a low-latency
  available node. A 20 ms tolerance avoids switching for insignificant changes.
- `IPV6-FIRST`: prefer available IPv6 nodes, with IPv4 nodes as backups.
- Any individual node: select a specific protocol and address family manually.

Automatic groups never fall back to `DIRECT`. Existing sessions may need to
reconnect after a node fails or changes; switching nodes is not connection
migration. Measurements run on the client, so the best node depends on its
current ISP and network.

## Configure public endpoints

Each inbound can retain its existing `server_address` and additionally set:

```json
{
  "listen": "::",
  "server_address": "203.0.113.10",
  "server_address_ipv4": "203.0.113.10",
  "server_address_ipv6": "2001:db8::10"
}
```

Use actual public addresses instead of these documentation examples. The two
new fields accept literal addresses of their respective families. They are
available in the inbound editor and create/update API. They do not change
listeners or open firewall rules: configure dual-stack listeners and allow
the protocol's TCP or UDP port in both host and provider firewalls.

The opt-in profile expands each supported inbound into IPv4 and IPv6 entries.
A missing family can use a primary literal address of that family. A primary
hostname with no explicit family overrides produces two entries with Mihomo's
`ip-version` setting; configure both A and AAAA records for both entries to
work. A lone literal address never invents an address for the other family.

TLS SNI, Reality keys and protocol credentials stay unchanged when the server
address changes. An IPv6 literal can use the same valid DNS certificate and
SNI as the IPv4 node. Do not disable certificate verification for this.

## Include multiple protocols

Subscriptions include enabled, unexpired client rows that explicitly share
the same UUID across enabled inbounds. Email labels do not merge users. Use a
fresh dedicated UUID for an intentionally combined subscription; reusing an
unrelated user's UUID grants access to all rows with that credential.

VLESS, VMess, Trojan, Shadowsocks, Hysteria2 and TUIC are supported. The helper
`scripts/setup_dual_stack_subscription.py` creates six separate inbounds and a
dedicated shared subscription account via the trusted Unix-socket API. It uses
classic `chacha20-ietf-poly1305` for Shadowsocks: SS-2022's base64 PSK cannot
serve as a VLESS/VMess UUID. Limits and accounting remain per client row; this
feature does not create a combined quota across protocols.

Run the helper on the panel host after issuing a valid ACME certificate. Use
`--help`, then `--dry-run` and `--preflight-only` with the intended arguments.
Keep its state directory root-only and outside the repository. It retains
ownership records for safe retries and refuses unrelated tag/client collisions.
Its normal run requires twelve successful local protocol probes (six protocols
over two address families) before writing the private HTTPS subscription URL.

Those probes verify the server's local data paths. Client-side node tests also
check the actual Internet path and provider firewall. IPv6 access to the VPS
does not force every destination connection leaving the VPS to use IPv6.

Normal Clash and Base64 URLs without `dual_stack=1` retain their existing
single-endpoint output.
