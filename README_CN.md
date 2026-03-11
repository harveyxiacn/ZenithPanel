<div align="center">
  <h1>🌌 <a href="https://github.com/harveyxiacn/ZenithPanel">ZenithPanel</a></h1>
  <p><b>下一代全能型服务器运维与代理服务管理面板</b></p>
  <p>不仅仅是 1Panel，不仅仅是 3x-ui，更是你的专属数字中枢。</p>

  <p>
    <a href="https://github.com/harveyxiacn/ZenithPanel/blob/main/LICENSE">
      <img src="https://img.shields.io/badge/License-MIT-blue.svg" alt="MIT License">
    </a>
  </p>

  <p>
    <b>简体中文</b> | <a href="README.md">English</a>
  </p>
</div>

---

## 📖 项目简介

**ZenithPanel** (*Zenith 意为巅峰、顶点*) 是一个旨在整合服务器日常运维、容器应用部署以及现代化代理服务（Xray-core / Sing-box）管理的**全功能 Web 仪表盘**。

目前的 Server 面板（如 1Panel、宝塔）往往偏向于建站和运维，高级功能甚至需要付费；而代理面板（如 3x-ui、Sing-box UI）则完全聚焦于代理，且各自配置孤立。ZenithPanel 致力于打破这一壁垒：**用一个极低资源占用、无商业限制的开源面板，解决 VPS 的所有核心需求**。

## ✨ 核心特性

结合对现有方案的深入分析，我们在 ZenithPanel 中进行了大量**架构与安全上的优化**：

### 🛠️ 1. 极致优化的运维体验 (1Panel 替代)
- **极简资源消耗**：采用 Go 语言构建，摆脱大量运行时依赖，最低 64MB 内存即可流畅运行。
- **Web 终端与文件管理**：内置高性能基于 WebSocket 的 SSH 终端，以及现代化的文件在线编辑器。
- **Docker 容器管家**：直观管理容器生命周期，内置“开箱即用”的开源应用商店（类似 1Panel，但不设专业版壁垒）。
- **系统监控与防护**：实时监控硬件资源状态，集成基础安全防火墙与 Fail2Ban。

### 🚀 2. 现代代理服务统一调度 (3x-ui & Sing-box UI 升级)
- **多核心无缝切换**：同时支持调度 **Xray-core** 和 **Sing-box**。无需手动切分端口，ZenithPanel 会帮你分配调度规则。
- **可视化路由编排**：抛弃枯燥难配置的 JSON 手写。采用节点可视连线或增强表单的方式配置复杂的出站路由及分流规则。
- **高级用户与流量管理**：支持多用户隔离、精细化流量限制、基于时间的过期机制。
- **全自动 TLS 证书 (ACME)**：真正“点一下”就能自动签发和续期的证书管理，无需繁杂的外部脚本。
- **协议全覆盖**：VLESS / Trojan / Hysteria2 / WireGuard / TUIC 一站式管理。

### 🛡️ 3. 企业级安全架构 (基础优化与加固)
- **原生安全机制**：内置 JWT 强认证、两步验证(2FA)、IP 白名单机制（解决原 Sing-box UI 无认证抓包裸奔问题）。
- **API 通信加密**：后端 API 强制内部随机生成密钥校验，杜绝非法跨域（移除原版不安全的 CORS `*` 设定）。
- **进程隔离沙箱**：代理内部分支运行在降权的非 root 用户组下，限制 Docker Socket 的暴露范围。

## 💻 技术栈架构

为了打造极具性能和视觉冲击力的面板，我们精选了以下现代技术栈：

- **后端 (Backend)**: `Go 1.24` + `Gin` / `Fiber` + `SQLite` (内嵌)
- **前端 (Frontend)**: `Vue 3` / `React` + `TypeScript` + `TailwindCSS` + `Radix UI / shadcn`
- **通信 (Message)**: 纯 WebSocket (终端、日志、监控实时推送)
- **交互层**: Docker SDK for Go (容器化自动化管理)
- **部署模式**: **单文件二进制 (All-in-One)** + 独立数据目录，零依赖部署。

## 🗺️ 架构优化概览 (相较于市面方案)

1. **从“拼凑组合”到“系统级整合”**：以前你需要装 Nginx、装 1Panel、装 3x-ui。现在 ZenithPanel 自带轻量量化网关，直接接管 80/443 端口，通过 SNI 智能分流代理流量与 Web 面板流量。
2. **状态统一持久化**：所有的配置不再散落于宿主机四处。所有代理配置、用户信息全部由 SQLite 数据库统一管理，面板自动渲染出最终的 `config.json` 进行热重载。
3. **备份迁移**：只需打包 `zenith-data` 一个文件夹即可全盘克隆到新机器。

---

## 🙏 致谢 (Acknowledgments)

作为一个站在前人肩膀上的开源项目，ZenithPanel 的诞生离不开以下优秀开源项目的启发与底层支持：

- **[1Panel](https://github.com/1Panel-dev/1Panel)**: 为我们提供了现代化服务器运维面板的架构思路与容器管理交互灵感。
- **[3x-ui](https://github.com/MHSanaei/3x-ui)**: 代理面板领域的优秀前驱，其丰富的功能为我们的代理核心设计提供了重要参考。
- **[Sing-box UI](https://github.com/hiddify/hiddify-next)**: 在基于 Sing-box 核心的面板交互上提供了宝贵的可视化经验。
- **[Xray-core](https://github.com/XTLS/Xray-core) & [Sing-box](https://github.com/SagerNet/sing-box)**: 强大的底层代理核心引擎，支撑了面板的高级路由与分流能力。

我们对这些项目的维护者和贡献者表示最诚挚的感谢！

---

## 📄 开源许可 (License)

本项目采用 [MIT License](LICENSE) 协议进行开源，您可以自由地使用、修改和分发。

---

> 快来看看吧，准备好迎接全新的 VPS 管理方式！欢迎提交 Issue 与 PR，共同打造这款开源神器！
