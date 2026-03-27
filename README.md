<div align="center">
  <h1>🌌 <a href="https://github.com/harveyxiacn/ZenithPanel">ZenithPanel</a></h1>
  <p><b>Next-Generation All-in-One Server Management & Proxy Orchestration Panel</b></p>
  <p>More than 1Panel, more than 3x-ui—it's your ultimate digital hub.</p>

  <p>
    <a href="https://github.com/harveyxiacn/ZenithPanel/blob/main/LICENSE">
      <img src="https://img.shields.io/badge/License-MIT-blue.svg" alt="MIT License">
    </a>
    <a href="https://github.com/harveyxiacn/ZenithPanel/actions/workflows/main.yml">
      <img src="https://github.com/harveyxiacn/ZenithPanel/actions/workflows/main.yml/badge.svg" alt="Build Status">
    </a>
    <a href="https://github.com/harveyxiacn/ZenithPanel/pkgs/container/zenithpanel">
      <img src="https://img.shields.io/badge/Docker-ghcr.io-blue?logo=docker" alt="Docker Image">
    </a>
  </p>

  <p>
    <b>English</b> | <a href="README_CN.md">简体中文</a>
  </p>
</div>

---

## 📖 Introduction

**ZenithPanel** (*Zenith meaning peak or summit*) is an **all-in-one Web dashboard** designed to integrate daily server maintenance, containerized application deployment, and modern proxy service management (Xray-core / Sing-box).

Current server panels (like 1Panel, BT) focus on website hosting and general maintenance, often charging for professional features. Meanwhile, proxy panels (like 3x-ui, Sing-box UI) focus solely on proxies with isolated configurations. ZenithPanel bridges this gap: **A single, low-resource, open-source panel with no commercial limits, solving all core VPS needs.**

---

## ✨ Implemented Features

### 🛠️ Server Management
- **System Monitoring**: Real-time CPU, memory, disk, network I/O, uptime, and load average — polled every 5 seconds.
- **Web Terminal**: WebSocket-based interactive terminal powered by **xterm.js**, accessible directly from the browser.
- **File Manager**: Breadcrumb directory navigation, online file editor with save — sandboxed to `/home`.
- **Docker Manager**: List containers with status badges, start / stop / restart / remove actions, auto-refreshed every 10 seconds.
- **Firewall (iptables)**: View INPUT chain rules, add rules by protocol/port/action/source, delete rules by line number.
- **Cron Scheduler**: Create, enable/disable, and delete scheduled tasks with standard cron expressions — persisted to SQLite.

### 🚀 Proxy Orchestration
- **Inbound Management**: Full CRUD for Xray / Sing-box inbound configs with protocol selector and JSON settings editor.
- **Client / User Management**: Add and remove clients per inbound, auto-generated UUID, and format-aware subscription link sharing.
- **Routing Rules**: Manage domain, geo, and outbound routing rules in a structured form.
- **Proxy Runtime Status**: See whether Xray is running and how many enabled nodes, users, and routing rules are active.

### 🛡️ Security
- **Secure Setup Wizard**: First-run generates a random one-time password and random URL entry point, invalidated after setup.
- **JWT Authentication**: Startup-generated 32-byte random secret persisted in SQLite — no hardcoded secrets.
- **bcrypt Password Hashing**: Admin password stored as a bcrypt hash, never plaintext.
- **Login Rate Limiting**: Max 5 login attempts per second; excess requests return HTTP 429.
- **Security Headers**: `X-Frame-Options`, `X-Content-Type-Options`, `X-XSS-Protection`, `Referrer-Policy` on all responses.
- **File Sandbox**: File manager restricted to `/home` via `filepath.Clean` + root path validation.
- **CORS Hardened**: `AllowCredentials: false` — no wildcard + credentials conflict.

### 🏗️ Architecture
- **Single Binary Distribution**: Vue 3 frontend embedded via `go:embed` — deploy one file, no `dist/` folder needed.
- **Graceful Shutdown**: HTTP server shuts down cleanly within 5 seconds on `SIGINT`/`SIGTERM`.
- **Singleton Managers**: DockerManager, XrayManager, SingboxManager initialized once at startup and injected into routes.

---

## 🗺️ Roadmap

| Feature | Status |
|---|---|
| System monitoring dashboard | ✅ Done |
| Web terminal (xterm.js) | ✅ Done |
| File manager | ✅ Done |
| Docker lifecycle management | ✅ Done |
| Firewall (iptables) | ✅ Done |
| Cron scheduler | ✅ Done |
| Inbound / Client / Routing CRUD | ✅ Done |
| JWT + bcrypt auth | ✅ Done |
| Real-time traffic charts (ECharts) | 🔜 Planned |
| ACME / Let's Encrypt (auto TLS) | 🔜 Planned |
| WARP WireGuard one-click outbound | 🔜 Planned |
| 2FA / IP Whitelist | 🔜 Planned |

---

## 🚀 Quick Start

### Docker (Recommended)

```bash
docker run -d \
  --name zenithpanel \
  --restart always \
  -p 8080:8080 \
  -v /opt/zenithpanel/data:/opt/zenithpanel/data \
  -v /var/run/docker.sock:/var/run/docker.sock \
  ghcr.io/harveyxiacn/zenithpanel:main
```

> For full proxy-node exposure, use `--network host` and follow the dedicated setup flow in [docs/proxy-setup-guide.md](docs/proxy-setup-guide.md).
> If you keep bridge networking, you must publish every inbound port manually or clients will import successfully but fail to connect.

Then open `http://<your-server-ip>:8080` in your browser.

On first run, the **Setup Wizard** will display a one-time password and a randomized URL in the container logs:

```bash
docker logs zenithpanel
```

### Build from Source

```bash
git clone https://github.com/harveyxiacn/ZenithPanel.git
cd ZenithPanel

# Build frontend
cd frontend && npm install && npm run build && cd ..

# Sync the built frontend into the backend embed directory
# (Docker does this automatically in the image build)
cp -r frontend/dist/* backend/internal/api/dist/

# Build backend (embeds frontend)
cd backend && go build -o zenithpanel . && cd ..

./backend/zenithpanel
```

---

## 💻 Tech Stack

- **Backend**: `Go 1.24` + `Gin` + `SQLite` (via GORM) + `go:embed`
- **Frontend**: `Vue 3` + `TypeScript` + `TailwindCSS` + `xterm.js`
- **Auth**: `JWT` + `bcrypt`
- **Proxy Engines**: `Xray-core` + `Sing-box`
- **Container**: `Docker SDK for Go`
- **Deployment**: Single binary, Docker image at `ghcr.io/harveyxiacn/zenithpanel`

---

## 🙏 Acknowledgments

ZenithPanel stands on the shoulders of giants:

- **[1Panel](https://github.com/1Panel-dev/1Panel)**: For modern architecture and container interaction inspiration.
- **[3x-ui](https://github.com/MHSanaei/3x-ui)**: For feature-rich proxy management reference.
- **[Sing-box UI](https://github.com/SpadesA99/singbox_ui)**: For visual experience in Sing-box core interactions.
- **[Xray-core](https://github.com/XTLS/Xray-core) & [Sing-box](https://github.com/SagerNet/sing-box)**: The powerful engine cores driving our panel.

---

## 📄 License

This project is licensed under the [MIT License](LICENSE).

---

> Issues and PRs are welcome! Let's build the best open-source VPS management panel together.
