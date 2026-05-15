# V2Ray / Xray Proxy Setup Guide

This guide walks you through setting up proxy nodes in ZenithPanel and importing them into client apps (Clash, V2RayN, etc.).

---

## 🚀 Recommended: Use Smart Deploy

If your goal is a **stable unique egress IP** for avoiding platform risk control (bank/exchange/e-commerce account geo & IP profiling), skip manual node configuration and use Smart Deploy:

1. Log into the panel and visit `/smart-deploy`
2. **Step 1** — panel auto-probes the environment (kernel, BBR, port availability, time sync, firewall, ...)
3. **Step 2** — pick one of four presets. **"Stable Egress" is the recommended default.**
4. **Step 3** — (optional) provide domain / Reality SNI / custom port
5. **Step 4** — preview the exact plan: protocols, tuning ops, cert mode, firewall ports, notes
6. **Step 5** — confirm and apply; takes ~30 seconds

Every deployment is reversible — one click rolls back all system changes (sysctl, systemd drop-ins, created inbounds).

### Preset selection guide

| Preset | Protocols | When to use |
|---|---|---|
| **Stable Egress** ⭐ | VLESS + Reality · TCP 443 | Fixed IP for fintech/e-commerce accounts; no domain required |
| **Speed** | Hysteria2 · UDP 443 | Low latency, high throughput; ACME cert if domain provided, otherwise self-signed + `insecure=1` |
| **Combo** | Reality (TCP) + Hysteria2 (UDP) | TCP + UDP inbounds so the client can switch per network |
| **Weak Network** | Hysteria2 + TUIC · two UDP ports | Mobile 4G/5G or lossy links |

### Why "Stable Egress" defeats platform risk control

Platform risk engines look at: IP stability, TLS fingerprint plausibility, datacenter/residential characteristics. The Stable Egress preset:

- **Single IP, single port** — no rotation like commercial proxy pools
- **Reality protocol** — handshake mimics a real site (default `www.microsoft.com`); TLS fingerprint matches Chrome
- **TCP 443** — the most universally accepted port; UDP-filtering banks won't block it
- **Your VPS's stable IP** — cleaner than shared residential proxies, more consistent than rotating pools

---

## Manual configuration (for advanced users)

If you want fine-grained control per node, need custom routing rules, or Smart Deploy's presets don't cover your use case, continue below.

---

## Prerequisites

- ZenithPanel deployed and running (see `development_guide.md`)
- A VPS with a public IP
- (Optional) A domain name pointed to your VPS for TLS

## Docker Run Command

Make sure your container is started with the required flags:

```bash
docker run -d \
  --name zenithpanel \
  --network host \
  --pid=host \
  --privileged \
  --restart unless-stopped \
  -v zenith-data:/opt/zenithpanel/data \
  -v /var/run/docker.sock:/var/run/docker.sock \
  ghcr.io/harveyxiacn/zenithpanel:main
```

> `--network host` is required so that Xray can listen on the VPS ports directly.

---

## Step 1: Create Inbound Nodes

### Option A: Quick Setup (Recommended)

The easiest way — one-click auto-configuration:

1. Navigate to **Proxy Services** > **Inbound Nodes** tab.
2. Click **Quick Setup** (or the call-to-action button when no nodes exist).
3. **Select**: Choose from 6 presets. Click **Use Recommended** for VLESS+Reality (best censorship resistance, no domain needed).
4. **Review**: All settings are pre-filled automatically:
   - Reality keys (X25519) and short IDs are generated server-side
   - WebSocket paths are randomized
   - Shadowsocks passwords are auto-generated
   - Expand any node to customize ports, domains, cert paths, etc.
5. Toggle **Add recommended routing rules** to auto-create ad-blocking and private IP rules.
6. Toggle **Create first client** to auto-create a user with subscription link.
7. Click **Create All** — done!

| Preset | Default Port | Domain Needed? | Engine | Notes |
|--------|-------------|---------------|--------|-------|
| VLESS + Reality | 443 | No | Xray / Sing-box | Most censorship-resistant |
| VLESS + WS + TLS | 2083 | Yes | Xray / Sing-box | CDN (Cloudflare) compatible |
| VMess + WS + TLS | 2087 | Yes | Xray / Sing-box | Wide client support |
| Trojan + TLS | 2096 | Yes | Xray / Sing-box | Simple, fast |
| Hysteria2 | 8443 | Recommended† | **Sing-box only** | UDP/QUIC, ultra fast |
| Shadowsocks | 8388 | No | Xray / Sing-box | Lightweight |

> † **Hysteria2 without a domain is supported** — leave the Domain field blank in Quick Setup and the panel falls back to a self-signed cert (CN = server IP). The subscription URL is automatically built with `insecure=1`, and clients must accept the untrusted cert. You lose strict TLS verification and the cert fingerprint is easy to flag via DPI; provide a domain (with a Let's Encrypt cert — see [qr_setup_guide.md §3](qr_setup_guide.md#3-issue-a-real-lets-encrypt-cert)) whenever you can.

> **Important**: Hysteria2 is only supported by the Sing-box engine. If you use Hysteria2 with Xray, the Hysteria2 inbound will be automatically skipped and a warning displayed. Switch to Sing-box engine via the **Apply** dropdown to use Hysteria2.

### Option B: Manual Setup (Advanced)

For full control, click **Add Node** and configure manually:

### Example: VLESS + TCP + TLS

| Field    | Value |
|----------|-------|
| Tag      | `vless-tcp-tls` |
| Protocol | `vless` |
| Listen   | `0.0.0.0` |
| Port     | `443` |

**Settings JSON** (protocol-specific config):
```json
{
  "decryption": "none",
  "flow": "xtls-rprx-vision"
}
```

**Stream JSON** (transport + TLS):
```json
{
  "network": "tcp",
  "security": "tls",
  "tlsSettings": {
    "serverName": "your-domain.com",
    "certificates": [
      {
        "certificateFile": "/etc/letsencrypt/live/your-domain.com/fullchain.pem",
        "keyFile": "/etc/letsencrypt/live/your-domain.com/privkey.pem"
      }
    ]
  }
}
```

### Example: VLESS + Reality (no domain needed)

| Field    | Value |
|----------|-------|
| Tag      | `vless-reality` |
| Protocol | `vless` |
| Port     | `443` |

**Settings JSON:**
```json
{
  "decryption": "none",
  "flow": "xtls-rprx-vision"
}
```

**Stream JSON:**
```json
{
  "network": "tcp",
  "security": "reality",
  "realitySettings": {
    "dest": "www.microsoft.com:443",
    "serverNames": ["www.microsoft.com"],
    "publicKey": "YOUR_PUBLIC_KEY",
    "privateKey": "YOUR_PRIVATE_KEY",
    "shortIds": ["abcd1234"]
  }
}
```

> **Tip**: Quick Setup auto-generates Reality keys. For manual setup, generate a key pair with: `xray x25519`
> Run this inside the container: `docker exec zenithpanel xray x25519`
> Or use the panel API: `POST /api/v1/proxy/generate-reality-keys`

### Example: VMess + WebSocket + TLS

| Field    | Value |
|----------|-------|
| Tag      | `vmess-ws-tls` |
| Protocol | `vmess` |
| Port     | `443` |

**Settings JSON:**
```json
{}
```

**Stream JSON:**
```json
{
  "network": "ws",
  "security": "tls",
  "wsSettings": {
    "path": "/vmws",
    "headers": {
      "Host": "your-domain.com"
    }
  },
  "tlsSettings": {
    "serverName": "your-domain.com",
    "certificates": [
      {
        "certificateFile": "/etc/letsencrypt/live/your-domain.com/fullchain.pem",
        "keyFile": "/etc/letsencrypt/live/your-domain.com/privkey.pem"
      }
    ]
  }
}
```

### Example: Trojan + TCP + TLS

| Field    | Value |
|----------|-------|
| Tag      | `trojan-tls` |
| Protocol | `trojan` |
| Port     | `443` |

**Settings JSON:**
```json
{}
```

**Stream JSON:**
```json
{
  "network": "tcp",
  "security": "tls",
  "tlsSettings": {
    "serverName": "your-domain.com",
    "certificates": [
      {
        "certificateFile": "/etc/letsencrypt/live/your-domain.com/fullchain.pem",
        "keyFile": "/etc/letsencrypt/live/your-domain.com/privkey.pem"
      }
    ]
  }
}
```

### Example: Shadowsocks

| Field    | Value |
|----------|-------|
| Tag      | `ss-aead` |
| Protocol | `shadowsocks` |
| Port     | `8388` |

**Settings JSON:**
```json
{
  "method": "2022-blake3-aes-128-gcm",
  "password": "your-server-password-here",
  "network": "tcp,udp"
}
```

---

## Step 2: Apply Configuration

After creating inbounds, click the **Apply Configuration** button at the top of the Proxy Services page. This generates the config and starts/restarts the proxy engine.

### Engine Selection

ZenithPanel supports two proxy engines:

| Engine | Command | Protocols |
|--------|---------|-----------|
| **Xray** (default) | `POST /api/v1/proxy/apply?engine=xray` | VLESS, VMess, Trojan, Shadowsocks |
| **Sing-box** | `POST /api/v1/proxy/apply?engine=singbox` | All of the above + **Hysteria2** |

If your inbounds include Hysteria2, you **must** use Sing-box. Xray will automatically skip unsupported protocols and display a warning.

### Crash Detection

If the proxy engine crashes on startup (bad config, port conflict, missing binary), the API now returns the error message with stderr output. The status endpoint also includes `xray_last_error` / `singbox_last_error` for debugging.

You can preview the generated config, inspect runtime status, or trigger apply through the API:
```
GET /api/v1/proxy/status
POST /api/v1/proxy/apply?engine=xray
POST /api/v1/proxy/apply?engine=singbox
GET /api/v1/proxy/config/xray
GET /api/v1/proxy/config/singbox
```

---

## Step 3: Create Users (Clients)

Navigate to **Users & Subs** tab and click **Add Client**.

| Field         | Value |
|---------------|-------|
| Email         | `user1@example.com` |
| Select Inbound| Choose the inbound created in Step 1 |
| Traffic Limit | `0` (unlimited) or total bytes (e.g., `107374182400` for 100GB) |

The UUID is auto-generated. After creation:
- Click **Sub Link** to open the format selector and copy an explicit subscription URL.
- Click **QR Code** to generate a scannable QR code for mobile clients (supports V2Ray/Base64 and Clash/YAML formats).

If the panel is accessed through a management hostname, reverse proxy, or tunnel that is different from the actual proxy node address, edit the inbound and set **Public Host / IP** before importing the subscription. This value controls the host embedded into Clash and V2Ray client configs.

---

## Step 4: Import into Client Apps

### Subscription URL Format
```
https://your-server:panel-port/api/v1/sub/USER_UUID
```

The panel auto-detects the client type from the `User-Agent` header:
- **Clash/Mihomo/Stash/Surge/Shadowrocket/Loon** -> Clash YAML format
- **V2RayN/V2RayNG/Others** -> Base64-encoded links

> The subscription request host is only a fallback. If your proxy node should use a different public hostname or IP, set **Public Host / IP** on the inbound so generated subscriptions point clients at the correct endpoint.

You can also force the format:
```
https://your-server:port/api/v1/sub/UUID?format=clash
https://your-server:port/api/v1/sub/UUID?format=base64
```

### Clash / Mihomo
1. Open Clash -> **Profiles**
2. Click **Import** or paste the subscription URL
3. Click **Update** to download the config
4. Select the **PROXY** group and choose a node

### V2RayN (Windows)
1. Open V2RayN -> **Subscription** -> **Subscription Settings**
2. Add a new subscription with the explicit Base64 URL from ZenithPanel:
   `https://your-server:port/api/v1/sub/UUID?format=base64`
3. Click **Update Subscription** -> the nodes will appear in the list
4. Right-click a node -> **Set as Active Server**

### V2RayNG (Android)
1. Open V2RayNG -> tap **+** -> **Import config from URL**
2. Paste the explicit Base64 URL from ZenithPanel:
   `https://your-server:port/api/v1/sub/UUID?format=base64`
3. Tap the play button to connect

### Shadowrocket (iOS)
1. Open Shadowrocket -> tap **+** at top-right
2. Choose **Subscribe** -> paste the URL
3. Tap to update, then select a node

---

## Step 5: Open Firewall Ports

Make sure the inbound ports are open. You can use ZenithPanel's built-in Firewall page, or via the terminal:

```bash
# Example: open port 443 for TCP
iptables -I INPUT -p tcp --dport 443 -j ACCEPT

# Example: open port 8388 for TCP+UDP
iptables -I INPUT -p tcp --dport 8388 -j ACCEPT
iptables -I INPUT -p udp --dport 8388 -j ACCEPT
```

---

## Routing Rules

Navigate to **Proxy Services** > **Routing Rules** tab to add rules that control how traffic is routed:

| Outbound Tag | Purpose |
|-------------|---------|
| `direct`    | Traffic goes directly (no proxy) |
| `block`     | Traffic is dropped |

Example: Block ads by domain:
- Domain: `geosite:category-ads-all`
- Outbound Tag: `block`

Example: Direct traffic to China:
- IP: `geoip:cn`
- Outbound Tag: `direct`

---

## Troubleshooting

**Xray/Sing-box fails to start:**
- The Apply button now shows the exact error message if the engine crashes on startup
- Check the status API: `GET /api/v1/proxy/status` — look for `xray_last_error` or `singbox_last_error`
- Check if the port is already in use: `netstat -tlnp | grep 443`
- Preview the generated config: `GET /api/v1/proxy/config/xray`
- Run manually: `xray run -c /opt/zenithpanel/data/xray_config.json`

**Xray keeps crashing with Hysteria2:**
- Hysteria2 is **not supported by Xray**. Switch to Sing-box engine: `POST /api/v1/proxy/apply?engine=singbox`
- Xray will automatically skip Hysteria2 inbounds and show a warning

**TLS certificate errors:**
- Ensure cert files exist inside the container. If using Let's Encrypt on the host, mount the cert directory:
  ```bash
  -v /etc/letsencrypt:/etc/letsencrypt:ro
  ```

**Client can't connect (DIRECT works but proxy doesn't):**
- Verify the firewall port is open on the VPS
- Verify the inbound is enabled and the engine is running (check status)
- Verify the client is enabled and not expired
- For Reality: ensure keys were generated (Quick Setup does this automatically)
- Check the subscription URL returns valid config: `curl -v https://server:port/api/v1/sub/UUID`
- For Clash: the subscription auto-adds a DIRECT rule for the server address to prevent proxy loops

**Reality key pair:**
Quick Setup generates keys automatically. For manual setup:
```bash
docker exec zenithpanel xray x25519
```
Or use the API: `POST /api/v1/proxy/generate-reality-keys` — returns `private_key`, `public_key`, and `short_id`.

Put the private key in the server's Stream JSON `realitySettings.privateKey`, and the public key in `realitySettings.publicKey` (this is what clients receive via subscription).
