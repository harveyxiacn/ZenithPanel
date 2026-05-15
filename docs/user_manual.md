# ZenithPanel - User Manual

[简体中文](user_manual_CN.md) | English

## 📖 Introduction
ZenithPanel is an all-in-one VPS management and proxy orchestration panel designed for international trade and travel geeks. It features a modern immersive UI based on Vue 3 + Tailwind CSS and a high-performance Go-driven backend with extremely low memory overhead, perfectly suited for 1C1G small VPS plans.

---

## 🚀 Getting Started & Installation

> [!TIP]
> **Since this project is now Public**, GitHub Actions is **completely free and unlimited**. Every code push to the `main` branch will trigger an automatic build.

### Option 1: Use GitHub Automated Build (Recommended)
If you want to use the pre-built Docker image from GitHub:
1. Push your code and check the **Actions** tab in the repository; wait for the build to complete.
2. In your server, pull and run the image (see Option 3).

### Option 2: Local Build & Upload (Bypass CI/CD)
Since ZenithPanel has extremely low resource overhead, you can compile it locally (Windows/Mac) to generate a standalone binary and upload it to your VPS:

1. **Local Packaging**
   In the project root, Windows users run `./scripts/build_release.ps1` in PowerShell (Mac/Linux users run `bash scripts/build_release.sh`).
   This will generate a `zenithpanel-release.tar.gz` file in the root directory.

2. **Upload to Server**
   Use SCP / SFTP or tools like BT Panel to upload these two files to the **same directory** on your VPS (e.g., `/root`):
   - `zenithpanel-release.tar.gz`
   - `scripts/install.sh`

3. **Run Installation on VPS**
   ```bash
   bash install.sh
   ```
This script will automatically extract the package, install the required Docker environment, and configure the service as a `systemd` daemon.

### Option 3: Docker / Docker Compose
```bash
docker run -d \
  --network=host \
  -v /opt/zenithpanel/data:/opt/zenithpanel/data \
  --name zenithpanel \
  --restart always \
  ghcr.io/harveyxiacn/zenithpanel:main
```
> Using `--network=host` is recommended because the panel generates a **random port** on first launch for security (prevents port scanning). Check `docker logs zenithpanel` for the assigned port and setup wizard URL.
>
> Alternatively, you can specify a fixed port via the `ZENITH_PORT` environment variable:
> ```bash
> docker run -d -e ZENITH_PORT=8080 -p 8080:8080 \
>   -v /opt/zenithpanel/data:/opt/zenithpanel/data \
>   --name zenithpanel --restart always \
>   ghcr.io/harveyxiacn/zenithpanel:main
> ```

---

## 🛡️ Security Setup Wizard
> **Important**: To prevent the panel from being exposed to the public internet without security, you must check the logs during the first run to get a temporary password and security entry link!

1. Run `docker logs zenithpanel` to find the random port, setup URL, and one-time password.
2. Open the URL provided in the logs (e.g., `http://your_ip:38291/zenith-setup-AbcD123`).
3. Log in using the 16-character **one-time password** generated in the logs.
4. In the setup wizard, set your official administrator **Username** and **Password**, and customize your future panel entrance path.
5. Once setup is complete, the initial URL will be permanently deactivated.

---

## ⚙️ Core Modules

### Dashboard
- Real-time preview of host status (CPU/RAM/Disk usage), connection history, and core process status.

### Servers (Server Management)
Replaces the bloated basic features of control panels like 1Panel with a lightweight entry:
- **Web Terminal**: Full-screen, low-latency WebSocket-based SSH simulation.
- **File Manager**: Runs securely in the `/home` sandbox to prevent unauthorized access; supports online editing and batch downloads.
- **Docker Daemon**: Manage all running containers and controls on a single page.

### Proxy Services
The core of this system—integrating V2ray/Xray and Sing-box engines.
1. **Nodes**: Supports multiple protocols for inbounds, port configuration, and real-time TLS certificate mounting.
2. **Users**: Configure clients for nodes with expiration dates and data usage statistics.
3. **Sub**: Copy dynamically generated subscription URLs. Supports Clash (YAML), Surge, or V2ray (Base64) with automatic User-Agent detection.

---

## 💡 Quick Setup: One-Click Node Configuration (Recommended)

The fastest way to get started — the **Quick Setup** wizard auto-generates recommended configurations with a single click:

1. Go to **Proxy Services** > **Inbound Nodes** tab.
2. Click the **Quick Setup** button (or use the call-to-action when the list is empty).
3. **Step 1 — Select**: Choose from 6 preset configurations (VLESS+Reality is recommended), or click **Use Recommended** for one-click setup.
4. **Step 2 — Review**: All settings are pre-filled (keys, paths, ports). Expand any node to customize parameters if needed. Toggle options for default routing rules (block ads/private IPs) and creating a first client.
5. **Step 3 — Done**: Everything is created automatically. Click **Apply Configuration** to activate.

### Available Presets

| Preset | Best For | Domain Needed? |
|--------|----------|---------------|
| VLESS + Reality | Most censorship-resistant | No |
| VLESS + WS + TLS | CDN (Cloudflare) friendly | Yes |
| VMess + WS + TLS | Maximum client compatibility | Yes |
| Trojan + TLS | Simple and fast | Yes |
| Hysteria2 | High-speed UDP/QUIC | Yes |
| Shadowsocks | Lightweight, easy setup | No |

> Reality key pairs and short IDs are auto-generated server-side. WebSocket paths and Shadowsocks passwords are also randomized automatically.

---

## 🔧 Advanced: Manual Node Setup

For full control, you can still manually configure nodes:

1. Go to the `Proxy` panel, select `Nodes -> Add Node`.
2. Choose a protocol, enter port, and provide Settings/Stream JSON manually.
3. Go to the `Users` interface to assign a user to this node.
4. In the `Subscriptions` panel, use the format-aware subscription link picker and update your client with the correct link type.

See the [Proxy Setup Guide](proxy-setup-guide.md) for detailed JSON examples per protocol.

---

## 📱 QR Codes for Subscription

> For a step-by-step walkthrough (Clash Meta / V2RayN scan flow + tips on
> replacing the self-signed test cert with a real Let's Encrypt one), see
> [qr_setup_guide.md](qr_setup_guide.md).


Each client in the **Users & Subs** tab has a **QR Code** button that generates scannable QR codes for mobile clients:

- **V2Ray / V2RayN** format: Generates a Base64 subscription QR code for V2RayN, V2RayNG, and Shadowrocket.
- **Clash / Mihomo** format: Generates a Clash YAML subscription QR code for Clash, Mihomo, and Stash.

You can switch between formats with the toggle, download the QR code as PNG, or copy the explicit V2Ray/Base64 or Clash/YAML subscription link directly from the modal.

---

## 🌐 Multi-Language Support

ZenithPanel supports 4 languages:
- **English** (default)
- **简体中文** (Simplified Chinese)
- **繁體中文** (Traditional Chinese)
- **日本語** (Japanese)

To switch language, click the language selector at the bottom of the sidebar. Your preference is saved automatically and persists across sessions. The panel auto-detects your browser language on first visit.

---

## 🖥️ Headless CLI (`zenithctl`)

Everything available in the Web UI is also reachable from a shell — useful
for automation, debugging, recovery when the UI is unreachable, and AI
agents that drive the panel via SSH.

### One-time setup on the panel host

```bash
# Make the CLI conveniently named
ln -sf /opt/zenithpanel/zenithpanel /usr/local/bin/zenithctl

# Mint a long-lived API token, write the profile, no password prompts.
# Only root can run this — it uses /run/zenithpanel.sock which is 0600.
zenithctl token bootstrap
```

After `token bootstrap` you'll see something like:

```
Token created and saved to /root/.config/zenithctl/config.toml
Name:   local-root-1778819000
Token:  ztk_AbCd…_4f9b21
Keep this token safe — it grants full panel access.
```

Subsequent invocations as root use the unix socket automatically, no
credentials needed. From a non-root shell on the same host, or from a
different machine, pass the token explicitly:

```bash
zenithctl --host https://panel.example.com --token ztk_AbCd…_4f9b21 status
```

Or add a profile to `~/.config/zenithctl/config.toml`:

```toml
default = "prod"

[profile.prod]
host       = "https://panel.example.com"
token      = "ztk_AbCd…_4f9b21"
verify_tls = true
```

### Daily commands

```bash
zenithctl status                                # ping panel + version snapshot
zenithctl inbound list                          # JSON dump of every inbound
zenithctl client add --inbound 1 --email alice  # provision a new user
zenithctl client list --inbound 1
zenithctl proxy status                          # which engines are running
zenithctl proxy apply                           # dual-engine reload
zenithctl proxy test 1                          # server-side probe of inbound #1
zenithctl proxy test all                        # probe every enabled inbound
zenithctl firewall list
zenithctl backup export --out backup.zip
zenithctl raw GET /api/v1/health                # escape hatch — call any endpoint
```

JSON is the default output; pass `-q` to print only the `data` field so
shell pipelines can consume it cleanly.

### Token management via Web UI

Tokens can also be created and revoked from **Settings → API Tokens**.
Each token has a name, optional scopes, and an optional expiry. The
plaintext is shown **once** at creation time — copy it immediately.

| Scope         | Grants                                           |
|---            |---                                               |
| `read`        | All `GET` endpoints                              |
| `write`       | Mutations on inbounds/clients/routing rules      |
| `proxy:apply` | Restart engines via `/proxy/apply`               |
| `system`      | BBR / swap / sysctl / cleanup                    |
| `firewall`    | iptables rules                                   |
| `backup`      | Export and restore                               |
| `admin`       | Admin endpoints incl. token CRUD, 2FA, password  |
| `*`           | Everything (default for `token bootstrap`)       |

A revoked token is rejected immediately and the row stays in the table
for audit. Bootstrap tokens issued via the unix socket are full-scope.

---

## ⚡ Dual-Engine Mode

ZenithPanel runs **Xray** and **Sing-box** concurrently by default. When
you call `POST /api/v1/proxy/apply` without an `engine=` query parameter
(which is what the Web UI's *Apply* button and `zenithctl proxy apply`
do), the panel partitions enabled inbounds across the two engines:

| Engine    | Protocols                                       |
|---        |---                                              |
| Xray      | VLESS, VMess, Trojan, Shadowsocks (TCP & WS+TLS)|
| Sing-box  | Hysteria2, TUIC (QUIC-only protocols)           |

Both processes run side-by-side, each bound to disjoint ports. Operators
forcing a single engine via `?engine=xray` or `?engine=singbox` get the
legacy behavior — useful for diagnostics.

The **Proxy** page header shows a 🌌 *Dual engines* badge when partitioning
is active, plus a 🔀 *N served by Sing-box* chip listing the protocols
Sing-box owns. The previous amber "skipped" warning only appears when an
operator deliberately runs single-engine.

---

## 🔍 Server-Side Inbound Probe

Each row in the **Inbound Nodes** table has a **Probe** button. Clicking
it runs a panel-local check that confirms the engine is actually serving
the port:

1. Reads `/proc/net/{tcp,udp}{,6}` to confirm the listener is bound.
2. For TCP-style inbounds (VLESS, VMess, Trojan, SS): connects to
   `127.0.0.1:port`, then drives a TLS handshake if `stream.security: tls`.
3. For QUIC-style inbounds (Hysteria2, TUIC): opens a connected UDP socket
   and sends a 16-byte sentinel; ECONNREFUSED means the engine isn't there.

The button turns into a chip showing either `✓ 73ms` (success) or
`✗ tcp/tls/udp` (the failing stage). Click the chip to re-probe. The
same check is exposed at `GET /api/v1/proxy/test/:inbound_id` for CLI
and automation use.

> ⚠ The probe only confirms that the panel-managed engine is serving the
> port from inside the host. It does **not** prove the inbound is reachable
> from the public Internet — for that, use a real client from outside.

---

## 📡 Protocol Connectivity Test Harness

`scripts/proto_sweep_dual.sh` on the panel host spins up one sing-box
client per protocol and curls the public Internet through each in turn.
Output is a PASS/FAIL line per inbound — useful as a smoke test after
upgrades or routing-rule changes.

See [protocol_connectivity_report.md](protocol_connectivity_report.md)
for the latest verified protocol matrix and reproduction steps.
