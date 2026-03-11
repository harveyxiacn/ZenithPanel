<div align="center">
  <h1>🌌 <a href="https://github.com/harveyxiacn/ZenithPanel">ZenithPanel</a></h1>
  <p><b>Next-Generation All-in-One Server Management & Proxy Orchestration Panel</b></p>
  <p>More than 1Panel, more than 3x-ui—it's your ultimate digital hub.</p>

  <p>
    <a href="https://github.com/harveyxiacn/ZenithPanel/blob/main/LICENSE">
      <img src="https://img.shields.io/badge/License-MIT-blue.svg" alt="MIT License">
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

## ✨ Key Features

Based on deep analysis of existing solutions, we implemented significant **architectural and security optimizations**:

### 🛠️ 1. Optimized Maintenance (1Panel Alternative)
- **Minimal Resource Usage**: Built with Go, zero heavy runtime dependencies, runs smoothly on as little as 64MB RAM.
- **Web Terminal & File Manager**: High-performance WebSocket-based SSH terminal and a modern online file editor.
- **Docker Manager**: Intuitive container lifecycle management with an "out-of-the-box" open-source App Store.
- **System Monitoring**: Real-time hardware resource monitoring integrated with basic firewall and Fail2Ban.

### 🚀 2. Unified Proxy Orchestration (3x-ui & Sing-box UI Upgrade)
- **Seamless Multi-Core Switching**: Supports both **Xray-core** and **Sing-box**. No more manual port management; ZenithPanel handles routing rules for you.
- **Visual Routing Designer**: Ditch cryptic JSON files. Design complex outbound routing and shunting rules via visual connections or enhanced forms.
- **Advanced User & Traffic Management**: Multi-user isolation, granular traffic limits, and time-based expiration.
- **Auto TLS (ACME)**: One-click certificate issuance and renewal without external scripts.
- **Full Protocol Support**: VLESS / Trojan / Hysteria2 / WireGuard / TUIC all managed in one place.

### 🛡️ 3. Enterprise-Grade Security
- **Native Security Mechanisms**: Built-in JWT authentication, 2FA, and IP whitelisting to prevent unauthorized access.
- **API Encryption**: Backend APIs use internally randomized secrets for validation, strictly prohibiting illegal Cross-Origin requests.
- **Process Isolation Sandbox**: Proxy internal processes run under non-root users, limiting Docker Socket exposure.

## 💻 Tech Stack

Chosen for performance and visual excellence:

- **Backend**: `Go 1.24` + `Gin` / `Fiber` + `SQLite` (Embedded)
- **Frontend**: `Vue 3` + `TypeScript` + `TailwindCSS` + `shadcn/ui`
- **Communication**: Pure WebSocket for real-time terminal, logs, and monitoring.
- **Interaction**: Docker SDK for Go for automated management.
- **Deployment**: **Single Binary (All-in-One)** + Independent data dir, zero-dependency deployment.

## 🗺️ Architectural Highlights

1. **From "Fragmented" to "Integrated"**: No need to install Nginx, 1Panel, and 3x-ui separately. ZenithPanel includes a lightweight gateway that handles 80/443 ports via SNI.
2. **Unified Persistence**: No scattered configurations. All settings and users are managed in a single SQLite database.
3. **Easy Backup**: Just back up the `zenith-data` folder to migrate everything to a new machine.

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

> Come and have a look! Ready for a new way to manage your VPS? Issues and PRs are welcome!
