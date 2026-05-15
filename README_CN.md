<div align="center">
  <h1>🌌 <a href="https://github.com/harveyxiacn/ZenithPanel">ZenithPanel</a></h1>
  <p><b>下一代全能型服务器运维与代理服务管理面板</b></p>
  <p>不仅仅是 1Panel，不仅仅是 3x-ui，更是你的专属数字中枢。</p>

  <p>
    <a href="https://github.com/harveyxiacn/ZenithPanel/blob/main/LICENSE">
      <img src="https://img.shields.io/badge/License-MIT-blue.svg" alt="MIT License">
    </a>
    <a href="https://github.com/harveyxiacn/ZenithPanel/actions/workflows/main.yml">
      <img src="https://github.com/harveyxiacn/ZenithPanel/actions/workflows/main.yml/badge.svg" alt="构建状态">
    </a>
    <a href="https://github.com/harveyxiacn/ZenithPanel/pkgs/container/zenithpanel">
      <img src="https://img.shields.io/badge/Docker-ghcr.io-blue?logo=docker" alt="Docker 镜像">
    </a>
  </p>

  <p>
    <b>简体中文</b> | <a href="README.md">English</a>
  </p>
</div>

---

## 📖 项目简介

**ZenithPanel** (*Zenith 意为巅峰、顶点*) 是一个旨在整合服务器日常运维、容器应用部署以及现代化代理服务（Xray-core / Sing-box）管理的**全功能 Web 仪表盘**。

目前的服务器面板（如 1Panel、宝塔）往往偏向于建站和运维，高级功能甚至需要付费；而代理面板（如 3x-ui、Sing-box UI）则完全聚焦于代理，且各自配置孤立。ZenithPanel 致力于打破这一壁垒：**用一个极低资源占用、无商业限制的开源面板，解决 VPS 的所有核心需求**。

---

## ✨ 已实现功能

### 🛠️ 服务器运维
- **系统监控**：实时展示 CPU、内存、磁盘、网络 I/O、运行时长及负载均值，每 5 秒自动刷新。
- **Web 终端**：基于 **xterm.js** + WebSocket 的浏览器内交互式终端，无需 SSH 客户端。
- **文件管理器**：面包屑式目录导航，支持在线文件编辑与保存，已通过路径沙箱限制在 `/home` 目录下。
- **Docker 容器管理**：容器列表含状态标签，支持启动 / 停止 / 重启 / 删除操作，每 10 秒自动刷新。
- **防火墙 (iptables)**：查看 INPUT 链规则，按协议/端口/动作/来源添加规则，按行号删除规则。
- **Cron 定时任务**：支持标准 Cron 表达式，可创建、启用/禁用、删除定时任务，数据持久化至 SQLite。

### 🚀 代理服务管理
- **入站 (Inbound) 管理**：Xray / Sing-box 入站配置的完整增删改查，含协议选择器与 JSON 设置编辑器。
- **协议覆盖**：VLESS（含 Reality）、VMess、Trojan、Shadowsocks（含 plugin 支持）、Hysteria2（含 salamander `obfs` 与端口跳跃）以及 **TUIC v5**（Sing-box）在双内核下均可使用；订阅链接与 Clash YAML 会按协议导出完整参数。
- **备份与恢复**：可将入站、客户端、路由规则、Cron 任务与非敏感设置打包导出为可迁移的 zip 归档；管理员凭证、JWT 密钥与 TLS 证书路径不会被导出，恢复后登录信息保持不变。
- **客户端 / 用户管理**：按入站添加和删除客户端，自动生成 UUID，并支持带格式选择的订阅链接分享。
- **路由规则管理**：以结构化表单管理域名、地理位置 (geo) 和出站路由规则。
- **代理运行状态**：可直接查看 Xray 是否运行，以及当前启用的节点、用户和路由规则数量。

### 🛡️ 安全机制
- **安全初始化向导**：首次运行自动生成一次性随机密码与随机 URL 入口，配置完成后自动失效。
- **JWT 认证**：启动时随机生成 32 位强密钥并持久化至 SQLite，无任何硬编码 Secret。
- **bcrypt 密码哈希**：管理员密码以 bcrypt 哈希存储，杜绝明文。
- **登录限流**：每秒最多 5 次登录尝试，超出返回 HTTP 429。
- **安全响应头**：全局注入 `X-Frame-Options`、`X-Content-Type-Options`、`X-XSS-Protection`、`Referrer-Policy`。
- **文件沙箱**：通过 `filepath.Clean` + 根路径校验，将文件管理权限严格限制在 `/home` 目录下。
- **CORS 加固**：`AllowCredentials: false`，消除通配符与凭证共存的安全隐患。

### 🏗️ 架构特性
- **单文件分发**：Vue 3 前端通过 `go:embed` 嵌入 Go 二进制，部署无需 `dist/` 目录。
- **低配 VPS 友好**：Gin 以 Release 模式运行；系统监控快照带 3s 缓存；订阅输出 8s 内命中缓存并使用 `clients JOIN inbounds` 单查询；代理引擎日志采用环形缓冲区避免长期运行内存增长；空闲限流器定期回收。详情见 [CHANGELOG.md](CHANGELOG.md)。
- **优雅停机**：监听 `SIGINT`/`SIGTERM` 信号，5 秒内完成 HTTP Server 的安全关闭。
- **单例管理器**：DockerManager、XrayManager、SingboxManager 启动时初始化单例并注入路由，防止资源泄露。

---

## 🗺️ 功能路线图

| 功能 | 状态 |
|---|---|
| 系统监控仪表盘 | ✅ 已完成 |
| Web 终端 (xterm.js) | ✅ 已完成 |
| 文件管理器 | ✅ 已完成 |
| Docker 容器生命周期管理 | ✅ 已完成 |
| 防火墙 (iptables) | ✅ 已完成 |
| Cron 定时任务调度器 | ✅ 已完成 |
| 入站 / 客户端 / 路由规则 CRUD | ✅ 已完成 |
| JWT + bcrypt 认证体系 | ✅ 已完成 |
| TUIC v5 协议支持（Sing-box） | ✅ 已完成 |
| 备份 / 恢复（可迁移归档） | ✅ 已完成 |
| 无头 CLI (`zenithctl`) + API Token | ✅ 已完成 |
| 双引擎并存 (Xray + Sing-box 同时跑) | ✅ 已完成 |
| 实时流量图表 (ECharts) | ✅ 已完成 |
| ACME / Let's Encrypt 自动 TLS + 自动续期 | ✅ 已完成 |
| 广告拦截一键开关（路由层） | ✅ 已完成 |
| Prometheus `/metrics` 端点 | ✅ 已完成 |
| WARP WireGuard 一键出站 | 🔜 规划中 |
| 两步验证 (2FA) / IP 白名单 | 🔜 规划中 |

---

## 🚀 快速开始

### Docker 部署（推荐）

```bash
docker run -d \
  --name zenithpanel \
  --restart always \
  -p 8080:8080 \
  -v /opt/zenithpanel/data:/opt/zenithpanel/data \
  -v /var/run/docker.sock:/var/run/docker.sock \
  ghcr.io/harveyxiacn/zenithpanel:main
```

> 如果要启用完整的代理节点监听能力，请使用 `--network host`，并参考 [docs/proxy-setup-guide-cn.md](docs/proxy-setup-guide-cn.md) 中的专用部署方式。
> 如果继续使用桥接网络，还需要手动映射每一个入站端口，否则客户端虽然能导入订阅，但实际连接会失败。

部署完成后，在浏览器打开 `http://<你的服务器IP>:8080`。

首次启动时，**初始化向导**会在容器日志中输出一次性密码和随机访问地址：

```bash
docker logs zenithpanel
```

### 可选：为 Hysteria2 / TUIC / WS-TLS 配 TLS 证书

QUIC 类协议（Hysteria2、TUIC）和 `ws+tls` 等变体都需要 TLS 证书。
两条路：

- **不配域名** — 面板自动回退到自签证书（CN = 服务器 IP）。快速配置里
  把"域名"留空即可；生成的订阅 URL 会自动带 `insecure=1`，客户端
  跳过证书校验。最省事，但失去严格 TLS 校验，证书指纹也容易被
  DPI 识别。
- **配域名** — 面板内置 ACME 流程会自动签 Let's Encrypt 证书并接到
  入站上。如果主机 80 端口被占用（反向代理、隧道等），改走
  `acme.sh + DNS-01 via DDNS`，详见
  [docs/qr_setup_guide_CN.md §3](docs/qr_setup_guide_CN.md#3-申请正式-lets-encrypt-证书)。
  acme.sh 支持的所有 DNS 服务商都能用（DuckDNS、Cloudflare、
  DNSPod 等）。

### 从源码构建

```bash
git clone https://github.com/harveyxiacn/ZenithPanel.git
cd ZenithPanel

# 构建前端
cd frontend && npm install && npm run build && cd ..

# 将前端产物同步到后端嵌入目录
# （Docker 镜像构建时会自动完成这一步）
cp -r frontend/dist/* backend/internal/api/dist/

# 构建后端（自动嵌入前端）
cd backend && go build -o zenithpanel . && cd ..

./backend/zenithpanel
```

---

## 🖥️ 无头 CLI

面板启动后，Web UI 上的所有操作都可以从命令行完成 —— 适用于自动化、故障排查
以及 Claude-Code 式的 AI 代理操作。

```bash
# 在面板宿主机上（root）：创建 token 并写入 ~/.config/zenithctl/config.toml
ln -sf /opt/zenithpanel/zenithpanel /usr/local/bin/zenithctl
zenithctl token bootstrap

# 日常使用
zenithctl status
zenithctl inbound list
zenithctl client add --inbound 1 --email alice@example.com
zenithctl proxy status
zenithctl proxy apply              # 双引擎重启
zenithctl raw GET /api/v1/clients  # 调用任意 API 的逃生口
```

跨主机使用：通过 `--host https://panel.example.com --token ztk_…` 或在
`~/.config/zenithctl/config.toml` 中预先配置一个 profile。完整参考：
[docs/cli_design_CN.md](docs/cli_design_CN.md) / [docs/cli_api_spec.md](docs/cli_api_spec.md)。

---

## 💻 技术栈

- **后端**: `Go 1.24` + `Gin` + `SQLite`（GORM）+ `go:embed`
- **前端**: `Vue 3` + `TypeScript` + `TailwindCSS` + `xterm.js`
- **认证**: `JWT` + `bcrypt`
- **代理内核**: `Xray-core` + `Sing-box`
- **容器交互**: `Docker SDK for Go`
- **部署**: 单一二进制文件，Docker 镜像托管于 `ghcr.io/harveyxiacn/zenithpanel`

---

## 🙏 致谢

作为一个站在前人肩膀上的开源项目，ZenithPanel 的诞生离不开以下优秀开源项目的启发与底层支持：

- **[1Panel](https://github.com/1Panel-dev/1Panel)**：为我们提供了现代化服务器运维面板的架构思路与容器管理交互灵感。
- **[3x-ui](https://github.com/MHSanaei/3x-ui)**：代理面板领域的优秀前驱，其丰富的功能为我们的代理核心设计提供了重要参考。
- **[Sing-box UI](https://github.com/SpadesA99/singbox_ui)**：在基于 Sing-box 核心的面板交互上提供了宝贵的可视化经验。
- **[Xray-core](https://github.com/XTLS/Xray-core) & [Sing-box](https://github.com/SagerNet/sing-box)**：强大的底层代理核心引擎，支撑了面板的高级路由与分流能力。

我们对这些项目的维护者和贡献者表示最诚挚的感谢！

---

## 📄 开源许可

本项目采用 [MIT License](LICENSE) 协议进行开源，您可以自由地使用、修改和分发。

---

> 欢迎提交 Issue 与 PR，共同打造最好的开源 VPS 管理面板！
