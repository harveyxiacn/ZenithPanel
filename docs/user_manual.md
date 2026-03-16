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
  -p 8080:8080 \
  -v /opt/zenithpanel/data:/opt/zenithpanel/data \
  --name zenithpanel \
  --restart always \
  ghcr.io/harveyxiacn/zenithpanel:latest
```
After running, use `docker logs zenithpanel` to find the setup wizard link and temporary security password.

---

## 🛡️ Security Setup Wizard
> **Important**: To prevent the panel from being exposed to the public internet without security, you must check the logs during the first run to get a temporary password and security entry link!

1. Open the URL provided in the logs (e.g., `http://your_ip:8080/zenith-setup-AbcD123`).
2. Log in using the 16-character **one-time password** generated in the logs.
3. In the setup wizard, set your official administrator **Username** and **Password**, and customize your future panel entrance path.
4. Once setup is complete, the initial URL will be permanently deactivated.

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
4. In the `Subscriptions` panel, click the copy button and update your client!

See the [Proxy Setup Guide](proxy-setup-guide.md) for detailed JSON examples per protocol.
