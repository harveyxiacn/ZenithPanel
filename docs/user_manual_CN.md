# ZenithPanel - 极客面板用户使用手册

简体中文 | [English](user_manual.md)

## 📖 简介
ZenithPanel 是一款面向外贸/商旅极客的全方位 VPS 管理与科学上网代理核心编排面板。提供基于 Vue 3 + Tailwind CSS 的现代化沉浸式 UI 和 Go 语言驱动的极低内存开销后端。极度适配 1C1G 小型 VPS 方案。

---

## 🚀 启动与安装

> [!TIP]
> **由于本项目现已公开 (Public)**，GitHub Actions 是**完全免费且无限量使用**的。每次代码 Push 到 `main` 分支都会触发自动构建。

### 方案一：使用 GitHub 自动构建镜像 (推荐)
如果您想直接使用 GitHub 自动构建好的镜像：
1. 代码 Push 后查看仓库的 **Actions** 标签页，等待构建完成。
2. 在您的服务器上拉取镜像并运行（见方案三）。

### 方案二：本地编译上传部署 (绕过 Github Actions，适用于免费账户)
由于 ZenithPanel 包含极低资源开销的前后端，您可以直接在本地（Windows/Mac）编译它，生成一个脱离依赖的绿色安装包并上传到 VPS：

1. **本地执行打包**
   在项目根目录下，Windows 用户在 PowerShell 执行 `./scripts/build_release.ps1` （Mac/Linux 执行 `bash scripts/build_release.sh`）。
   这会在根目录生成一个 `zenithpanel-release.tar.gz` 文件。

2. **上传到服务器**
   使用 SCP / SFTP / 宝塔 等工具将以下两个文件上传到 VPS 的**同一个目录** (如 `/root`):
   - `zenithpanel-release.tar.gz`
   - `scripts/install.sh`

3. **进入 VPS 执行安装**
   ```bash
   bash install.sh
   ```
这一步脚本会自动解压包，安装所需的 Docker 环境，并将服务端配置为 `systemd` 守护进程开机自启。

### 方案三：Docker / Docker Compose 启动
```bash
docker run -d \
  --network=host \
  -v /opt/zenithpanel/data:/opt/zenithpanel/data \
  --name zenithpanel \
  --restart always \
  ghcr.io/harveyxiacn/zenithpanel:main
```
> 推荐使用 `--network=host`，因为面板首次启动时会生成**随机端口**以增强安全性（防止端口扫描）。执行 `docker logs zenithpanel` 查看分配的端口和初始化向导地址。
>
> 如需指定固定端口，可通过 `ZENITH_PORT` 环境变量设置：
> ```bash
> docker run -d -e ZENITH_PORT=8080 -p 8080:8080 \
>   -v /opt/zenithpanel/data:/opt/zenithpanel/data \
>   --name zenithpanel --restart always \
>   ghcr.io/harveyxiacn/zenithpanel:main
> ```

---

## 🛡️ 首次安全初始化向导 (Setup Wizard)
> **非常重要**：为了防止面板直接暴露公网导致配置和服务器失陷，首次运行必须在终端查看日志以获取临时密码和安全短链！

1. 执行 `docker logs zenithpanel` 查看随机端口、初始化 URL 和一次性密码。
2. 浏览器打开日志提示的 URL，如：`http://ip:38291/zenith-setup-AbcD123`
3. 使用日志内生成的 16 位**一次性密码**登录向导。
4. 在系统向导中设置正式的管理员 **账户名** 与 **密码**，并自定义后续的面版入口路径。
5. Setup 成功完成后，以上初始 URL 彻底失效！

---

## ⚙️ 核心功能模块

### Dashboard (系统展示大盘)
- 实时预览您的底层主机状态 (CPU / 内存 / 磁盘使用率) 以及历史连通性、核心进程运行状态。

### 服务器管理 (Servers)
完全替代了例如 1Panel 等控制面板的臃肿基础功能，保留最轻巧的系统入口：
- **Web Terminal**: 全屏沉浸、低延迟的基于 WebSocket 的系统后台仿真 SSH。
- **File Manager**: 安全运行在 `/home` 沙箱，防止越权，随时查看和批量下载编辑配置文件。
- **Docker 守护进程**: 一页管理您的所有运行容器、控制开关。

### 代理节点分发中心 (Proxy Services)
这套系统的核心特色——全方位融合 V2ray/Xray 和 Sing-box 两大核心网络通信基石引擎。
1. **Nodes (入站节点)**：支持开启各协议入站并配置端口，并实时挂载证书进行 TLS 连接鉴权。
2. **Users**: 针对节点配置客户并附带到期时间控制和历史上下行流量记录。
3. **Sub (聚合订阅)**：一键复制动态生成的订阅 URL！无论是移动端采用 Clash(yaml)、Surge 或安卓采用的 V2ray(Base64)，客户端 UA 全端适配下发最新配置图谱。

---

## 💡 快速配置：一键创建节点（推荐）

最快速的上手方式——**快速配置向导**一键自动生成推荐配置：

1. 进入 **代理服务（Proxy Services）** > **入站节点（Inbound Nodes）** 标签页。
2. 点击 **Quick Setup（快速配置）** 按钮（节点列表为空时也会显示快速配置入口）。
3. **第一步 — 选择方案**：从 6 种预设配置中选择（推荐 VLESS+Reality），或点击 **Use Recommended** 一键选择最佳方案。
4. **第二步 — 检查与自定义**：所有配置已自动填充（密钥、路径、端口等）。展开任意节点可自定义参数。可选开启默认路由规则（屏蔽广告/私有IP）和自动创建首个客户端。
5. **第三步 — 完成**：所有节点自动创建完毕。点击 **Apply Configuration** 激活配置即可使用。

### 可用预设方案

| 方案 | 最适场景 | 是否需要域名 |
|------|---------|------------|
| VLESS + Reality | 抗封锁能力最强 | 否 |
| VLESS + WS + TLS | 支持 CDN（Cloudflare）转发 | 是 |
| VMess + WS + TLS | 客户端兼容性最广 | 是 |
| Trojan + TLS | 简单高速 | 是 |
| Hysteria2 | UDP/QUIC 高速传输 | 是 |
| Shadowsocks | 轻量级，易部署 | 否 |

> Reality 密钥对和 Short ID 由服务端自动生成。WebSocket 路径和 Shadowsocks 密码也会自动随机化。

---

## 🔧 进阶：手动节点配置

如需完全自定义控制，仍可手动配置节点：

1. 进入 `Proxy` 面板，选择 `Nodes -> Add Node`
2. 选择协议，输入端口，手动填写 Settings/Stream JSON
3. 前往 `Users` 界面为此节点分配用户
4. 进入 `Subscriptions` 面板，使用带格式选择的订阅链接复制功能，并把正确格式的链接导入客户端

详细的 JSON 配置示例请参见[代理设置指南](proxy-setup-guide-cn.md)。

---

## 📱 订阅二维码

> 想看一步步的"扫码到 Clash Meta / V2RayN" + "换正式 Let's Encrypt 证书"
> 操作图文？见 [qr_setup_guide_CN.md](qr_setup_guide_CN.md)。


**用户与订阅（Users & Subs）** 标签页中，每个客户端都有 **QR Code** 按钮，可生成手机客户端可扫描的二维码：

- **V2Ray / V2RayN** 格式：生成 Base64 订阅二维码，适用于 V2RayN、V2RayNG、Shadowrocket。
- **Clash / Mihomo** 格式：生成 Clash YAML 订阅二维码，适用于 Clash、Mihomo、Stash。

支持切换格式、下载 PNG 图片，或直接从弹窗复制明确的 V2Ray/Base64 或 Clash/YAML 订阅链接。

---

## 🌐 多语言支持

ZenithPanel 支持 4 种语言：
- **English**（英语，默认）
- **简体中文**
- **繁體中文**（繁体中文）
- **日本語**（日语）

切换语言请点击侧边栏底部的语言选择器。您的偏好会自动保存，跨会话持久化。首次访问时面板会自动检测浏览器语言。

---

## 🖥️ 无头 CLI（`zenithctl`）

Web UI 上能做的所有事情，命令行同样可以完成。适用于自动化脚本、故障排查、
Web UI 不可达时的应急救援，以及通过 SSH 驱动面板的 AI 代理。

### 面板宿主机一次性配置

```bash
# 命名 CLI 入口
ln -sf /opt/zenithpanel/zenithpanel /usr/local/bin/zenithctl

# 自助创建一个长生命周期 token，并写入 profile。仅 root 可执行——它使用
# 0600 权限的 /run/zenithpanel.sock。
zenithctl token bootstrap
```

`token bootstrap` 后会打印类似：

```
Token created and saved to /root/.config/zenithctl/config.toml
Name:   local-root-1778819000
Token:  ztk_AbCd…_4f9b21
Keep this token safe — it grants full panel access.
```

后续以 root 身份再次运行时会自动走 unix socket，无需任何凭证。从非 root
shell 或另一台机器调用，需要显式传 token：

```bash
zenithctl --host https://panel.example.com --token ztk_AbCd…_4f9b21 status
```

也可以在 `~/.config/zenithctl/config.toml` 加 profile：

```toml
default = "prod"

[profile.prod]
host       = "https://panel.example.com"
token      = "ztk_AbCd…_4f9b21"
verify_tls = true
```

### 日常命令

```bash
zenithctl status                                # ping 面板 + 版本快照
zenithctl inbound list                          # 入站 JSON dump
zenithctl client add --inbound 1 --email alice  # 新增用户
zenithctl client list --inbound 1
zenithctl proxy status                          # 引擎运行状态
zenithctl proxy apply                           # 双引擎重载
zenithctl proxy test 1                          # 探活入站 #1
zenithctl proxy test all                        # 探活所有启用的入站
zenithctl firewall list
zenithctl backup export --out backup.zip
zenithctl raw GET /api/v1/health                # 调用任意 API 的逃生口
```

默认输出 JSON；加 `-q` 只输出 `data` 字段，便于管道传递。

### 通过 Web UI 管理 Token

进入 **Settings → API Tokens** 也可创建/吊销 token。每个 token 有名称、
可选作用域、可选有效期。明文 token **只在创建时显示一次**——请立即复制。

| 作用域        | 授予                                          |
|---            |---                                            |
| `read`        | 所有 `GET` 端点                                |
| `write`       | 入站/客户端/路由规则的变更                      |
| `proxy:apply` | 通过 `/proxy/apply` 重启引擎                   |
| `system`      | BBR / swap / sysctl / 清理                     |
| `firewall`    | iptables 规则                                  |
| `backup`      | 导出与恢复                                     |
| `admin`       | 包含 token CRUD、2FA、密码的所有管理端点        |
| `*`           | 全部（`token bootstrap` 的默认）                |

被吊销的 token 立即失效，但记录保留供审计。通过 unix socket bootstrap 出来
的 token 是全权限的。

---

## ⚡ 双引擎模式

ZenithPanel 默认 **Xray** 与 **Sing-box** 同时运行。当你调用
`POST /api/v1/proxy/apply` 不带 `engine=` 参数时（Web UI 的 *Apply* 按钮
与 `zenithctl proxy apply` 都走这条路径），面板会自动分区：

| 引擎      | 协议                                            |
|---        |---                                              |
| Xray      | VLESS、VMess、Trojan、Shadowsocks（TCP/WS+TLS） |
| Sing-box  | Hysteria2、TUIC（QUIC 类协议）                  |

两个进程并行运行，绑定不同端口。操作员通过 `?engine=xray` 或
`?engine=singbox` 强制单引擎仍然可用——主要用于排障。

**Proxy** 页头部在双引擎模式下会显示一个 🌌 *双引擎* 徽章，外加 🔀
*N 个由 Sing-box 接管* 芯片，列出 Sing-box 承担的协议。原本的琥珀色
"协议被跳过"警告仅在用户主动选择单引擎时出现。

---

## 🛡️ 广告拦截（路由层）

**设置 → 广告拦截** 一键开关：开启后面板自动插入一条 managed 路由规则，
把 `geosite:category-ads-all` 流量送进 `block` 出口。两个引擎自动重载，
立即生效，无需手动 *应用配置*。

> 路由层广告拦截**无法剥离 YouTube 视频中的服务端拼接广告**——广告与
> 视频走同一个 `googlevideo.com` CDN。要彻底去除 YouTube 广告需要 DOM
> 级方案：浏览器装 uBlock Origin、Android TV 用 SmartTube、手机用
> ReVanced。

CLI 控制：`zenithctl raw PUT /api/v1/admin/adblock --data '{"enabled":true}'`。

---

## 🔍 服务端入站探活

**入站节点** 表格每行都有一个 **探活** 按钮。点击后面板会在本机做一次
连通性检查：

1. 读 `/proc/net/{tcp,udp}{,6}`，确认监听器已绑定。
2. TCP 类入站（VLESS / VMess / Trojan / SS）：连接 `127.0.0.1:port`，
   若 `stream.security: tls` 则继续做 TLS 握手。
3. QUIC 类入站（Hysteria2 / TUIC）：打开一个连接的 UDP socket 并发送
   16 字节哨兵；ECONNREFUSED 表示引擎不在。

按钮会变成芯片：`✓ 73ms`（成功）或 `✗ tcp/tls/udp`（失败阶段）。点击芯片
重新探活。同一检查通过 `GET /api/v1/proxy/test/:inbound_id` 暴露给 CLI
与自动化使用。

> ⚠ 探活只能确认面板托管的引擎正在本机监听端口。它**不能**证明该入站
> 从公网可达——后者需要用真实客户端从外部测试。

---

## 📡 协议连通性测试脚本

面板宿主机上 `scripts/proto_sweep_dual.sh` 会为每个协议起一个 sing-box
客户端，依次通过 SOCKS5 curl 公网 URL。输出按入站逐行 PASS/FAIL，适合
升级或路由规则变更后的烟雾测试。

最新的协议矩阵与复现步骤见
[protocol_connectivity_report.md](protocol_connectivity_report.md)。
