# V2Ray / Xray Proxy Setup Guide

This guide walks you through setting up proxy nodes in ZenithPanel and importing them into client apps (Clash, V2RayN, etc.).

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

## Step 1: Create an Inbound (Proxy Node)

Navigate to **Proxy Services** > **Inbound Nodes** tab and click **Add Node**.

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

> Generate Reality key pair with: `xray x25519`
> Run this inside the container: `docker exec zenithpanel xray x25519`

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

After creating inbounds, click the **Apply Configuration** button at the top of the Proxy Services page. This generates the Xray config and starts/restarts the Xray process.

You can preview the generated config at **Proxy Services** > click the "Apply Configuration" button, or call the API:
```
GET /api/v1/proxy/config/xray
```

---

## Step 3: Create Users (Clients)

Navigate to **Users & Subs** tab and click **Add Client**.

| Field         | Value |
|---------------|-------|
| Email         | `user1@example.com` |
| Select Inbound| Choose the inbound created in Step 1 |
| Traffic Limit | `0` (unlimited) or bytes (e.g., `107374182400` for 100GB) |

The UUID is auto-generated. After creation, click **Sub Link** to copy the subscription URL.

---

## Step 4: Import into Client Apps

### Subscription URL Format
```
https://your-server:panel-port/api/v1/sub/USER_UUID
```

The panel auto-detects the client type from the `User-Agent` header:
- **Clash/Mihomo/Stash/Surge/Shadowrocket/Loon** -> Clash YAML format
- **V2RayN/V2RayNG/Others** -> Base64-encoded links

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
2. Add a new subscription with the URL
3. Click **Update Subscription** -> the nodes will appear in the list
4. Right-click a node -> **Set as Active Server**

### V2RayNG (Android)
1. Open V2RayNG -> tap **+** -> **Import config from URL**
2. Paste the subscription URL
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

**Xray fails to start:**
- Check if the port is already in use: `netstat -tlnp | grep 443`
- Check Xray logs in the terminal: `cat /opt/zenithpanel/xray_config.json` to verify config
- Run manually: `xray run -c /opt/zenithpanel/xray_config.json`

**TLS certificate errors:**
- Ensure cert files exist inside the container. If using Let's Encrypt on the host, mount the cert directory:
  ```bash
  -v /etc/letsencrypt:/etc/letsencrypt:ro
  ```

**Client can't connect:**
- Verify the firewall port is open
- Verify the inbound is enabled
- Verify the client is enabled and not expired
- Check that the subscription URL is accessible

**Reality key pair:**
```bash
docker exec zenithpanel xray x25519
```
This outputs a private key and public key. Put the private key in the server's Stream JSON `realitySettings.privateKey`, and the public key in `realitySettings.publicKey` (this is what clients receive via subscription).
