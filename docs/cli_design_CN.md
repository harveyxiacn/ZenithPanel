# ZenithPanel CLI / Headless API 设计文档

[English](cli_design.md) | 简体中文

> 状态：设计稿 — 实施进度跟踪于 [plans/](plans/) 与 `task.md`。
> 受众：维护者、自动化用户、SSH 进入 VPS 后驱动面板的 Claude Code 代理。

## 1. 动机

当前 ZenithPanel 完全依赖 Web UI。没有一条头等的命令行通道。具体痛点：

- **运维流程** — SSH 在 VPS 上的维护者希望添加客户端、重载代理或查看状态，
  而不必再开浏览器。
- **无界面自动化** — CI、cron、远程运行器以及 AI 代理（Claude Code）需要一个
  与终端 UI 无关的确定性接口。
- **故障自救** — 面板 TLS 损坏或随机端口丢失时，仍需要一条本机救援通道。

设计为此引入一个新的 CLI 入口（`zenithpanel ctl …`，附带软链
`zenithctl`）以及少量新的 HTTP 端点。**不修改任何现有 Web-UI 行为**。

## 2. 设计目标

1. **单二进制** — CLI 作为现有 `zenithpanel` 二进制的子命令，无需额外发布物。
2. **本机零门槛** — 同主机 root 跑任何命令都无需登录流程。
3. **远程默认安全** — 跨主机必须用显式、可吊销、可分作用域的 API token，
   绝不使用长生命周期 JWT。
4. **零业务逻辑下沉** — CLI 命令在能做到时 1:1 映射到现有 HTTP handler，
   CLI 仅是薄客户端。
5. **可审计** — 每一次会产生状态变更的 CLI 调用都会以 `token:<name>` 或
   `local-root` 主体落入 `AuditLog`。
6. **可组合** — 默认 JSON 输出便于脚本；TTY 下可用 `--output table` 切到人可读
   表格。

## 3. 架构

```
                  ┌──────────────────────────────────────────────┐
                  │             zenithpanel 二进制               │
                  │                                              │
   argv[1]=ctl ──▶│  cli.Run() ── 读取 ~/.config/zenithctl ───┐ │
                  │      │                                     │ │
                  │      ▼                                     │ │
                  │  HTTP 客户端 ── 选择传输 ──────────────────┤ │
                  │                                            │ │
                  │   ┌─ unix:///run/zenithpanel.sock ─────────┼─┼──▶ 同一 gin
                  │   └─ https://panel.example/api/v1 ────────┘ │   引擎
                  │                                              │   (trusted_local
   无 argv[1] ──▶ │  server.Run() → 监听 TCP + unix socket     │    或 bearer token)
                  └──────────────────────────────────────────────┘
```

- **两个监听器、一个引擎。** `main.go` 一次性构造 gin router，分别挂在
  TCP `http.Server` 和以 `net.UnixListener` 为 Listener 的第二个 `http.Server`
  之上。Unix socket 路径为 `/run/zenithpanel.sock`，所有者 `root:root`，权限 `0600`。
  Unix listener 上挂一个中间件，将 `c.Set("trusted_local", true)`。
- **CLI 模式。** 当 `argv[1] == "ctl"`，`main.go` 直接进入 `cli.Run(argv)`，
  不初始化 DB、调度器或代理管理器。CLI 不引用任何 service 包，只走 HTTP。
- **传输选择。** CLI 按以下优先级选择目标：
  1. `--host` / `--socket` 显式参数；
  2. `$ZENITHCTL_HOST`；
  3. `~/.config/zenithctl/config.toml`；
  4. 默认 `/run/zenithpanel.sock`（文件存在且可达）；
  5. 否则报错并提示运行 `zenithctl token bootstrap` 或显式传 `--host`。

## 4. 认证模型

三类主体共存：

| 主体           | 建立方式                                | 请求侧鉴权                       | 审计名             |
|---             |---                                      |---                               |---                 |
| `local-root`   | 连接落到 unix socket                    | 无（由文件权限保证）             | `local-root`       |
| `token:<name>` | 管理员签发的 API token                  | `Authorization: Bearer ztk_…`    | `token:<name>`     |
| `admin:<u>`    | 浏览器会话                              | `Authorization: Bearer <JWT>`    | `admin:<u>`        |

### 4.1 Token 格式

```
ztk_<22 字符 base64url(16 随机字节)>_<6 字符 crc32 校验位>
```

服务端用 `crypto/rand` 生成。明文 token **仅在创建时一次性返回**；持久层只存
`sha256(token)`。末位 6 字符 crc32 用于客户端快速识别拼写错误。

### 4.2 数据模式

```go
type ApiToken struct {
    ID         uint
    Name       string    // 唯一，用户自取
    TokenHash  string    // 明文 token 的 sha256
    Scopes     string    // 逗号分隔，详见 §6
    ExpiresAt  int64     // unix 秒，0 = 不过期
    LastUsedAt int64
    Revoked    bool
    CreatedAt  time.Time
    UpdatedAt  time.Time
}
```

### 4.3 中间件

`middleware.AuthMiddleware()` 在需要同时支持浏览器与 CLI 的路由上取代
`JWTAuthMiddleware`：

```
若 c.GetBool("trusted_local") { 主体=local-root; 放行 }
若 Authorization 以 "Bearer ztk_" 开头 { 校验 token，主体=token:<name>; 放行 }
若 Authorization 以 "Bearer " 开头     { 走原 JWT 路径 }
否则 401
```

老路由继续工作：浏览器送 JWT；本机 CLI 落 unix socket 得 `trusted_local`；
远程 `zenithctl` 送 API token。

### 4.4 审计日志

变更类 handler 已会调用审计记录器。把记录器读取的字段从
`c.GetString("username")` 改为 `c.GetString("principal")`，找不到再回退
原字段，保持兼容。

## 5. CLI 命令树

顶层：`zenithctl <group> <verb> [flags]`。

```
zenithctl                                       — 帮助
zenithctl version
zenithctl status                                — ping /api/v1/health
zenithctl login                                 — 交互式 密码+TOTP，缓存 JWT（远程用）
zenithctl logout
zenithctl token list
zenithctl token create   --name X [--scopes …] [--expires-in 90d]
zenithctl token revoke   <id|name>
zenithctl token rotate   <name>                 — 签发 v2、吊销 v1、就地更新当前 profile（自身 token 也可安全轮换）
zenithctl token bootstrap                       — 仅 local-root；自助创建 token 并写入配置
zenithctl system info
zenithctl system bbr (status|enable|disable)
zenithctl system swap (status|create|remove) [--size 1G]
zenithctl inbound list [--json]
zenithctl inbound show     <id>
zenithctl inbound create   --file inbound.json
zenithctl inbound update   <id> --file inbound.json
zenithctl inbound set-port <id> <port> [--sync-firewall]
zenithctl inbound delete   <id>
zenithctl client list [--inbound <id>]
zenithctl client add     --inbound <id> --email foo [--uuid …] [--expires …]
zenithctl client delete  <id>
zenithctl proxy status
zenithctl proxy apply                           — 重新渲染配置并 reload xray/sing-box
zenithctl proxy test     <id> | all             — 服务端连通性探活
zenithctl proxy config (xray|singbox)
zenithctl cert  issue    --domain X --email Y   — ACME / Let's Encrypt 一键签证书
zenithctl backup restore --file backup.zip      — 上传并替换入站/客户端/路由/Cron
zenithctl proxy reality-keys
zenithctl proxy test <inbound-id>               — 服务端自检入站连通性
zenithctl sub url <client-uuid>
zenithctl firewall list
zenithctl firewall add    --port 443 --proto tcp --action ACCEPT [--source …]
zenithctl firewall delete <line-no>
zenithctl backup export   [--out backup.zip]
zenithctl backup restore  --file backup.zip
zenithctl raw <METHOD> <PATH> [--data @file|-]  — 调用任意 API 的逃生口
```

全局参数：`--host`、`--socket`、`--token`、`--output (json|table|yaml)`、
`-q/--quiet`、`--no-color`。

## 6. 作用域

Token 携带逗号分隔的作用域（`*` = 全权）。处理器内调用一个小助手做校验：

| 作用域        | 授予                                          |
|---            |---                                            |
| `read`        | 所有 `GET`                                    |
| `write`       | `/inbounds /clients /routing-rules` 变更       |
| `proxy:apply` | `POST /proxy/apply`、reload                   |
| `system`      | BBR/swap/sysctl、清理                          |
| `firewall`    | iptables 规则                                  |
| `backup`      | 导出与恢复                                     |
| `admin`       | `admin/*`，含 2FA、改密、TLS                    |
| `*`           | 全部                                           |

`token bootstrap` 与 Web UI 默认签 `*`。自动化场景建议显式收窄。

## 7. 安全注记

- **Socket 权限。** Listener 绑定后立即 `chmod 0600 root:root`；能 stat/connect
  socket 的人本就拥有该机 root 权限，信任级别一致。
- **不记录明文 token。** 仅创建时一次性打印；审计日志不会出现明文。
- **常数时间比较。** Hash 比对走 `subtle.ConstantTimeCompare`。
- **限流。** Bearer token 失败共享既有的按 IP 限流。
- **CSRF 不相关。** API token 经 `Authorization` 头携带，不写入 Cookie，
  浏览器跨域 CSRF 无法重放。
- **锁定隔离。** Token 失败 **不** 计入管理员密码锁定计数，避免有人通过
  乱发 token 把合法操作员锁出。

## 8. 迁移 / 兼容

- 新增 `api_tokens` 走 `AutoMigrate`，失败非致命（沿用现有模式）。
- 所有既有端点保留 JWT 鉴权；CLI 走同一端点，只是中间件 **额外** 接受两类
  新主体。
- 调用 `zenithpanel` 不带 `ctl` 子命令时行为 100% 不变。

## 9. 暂不实现

- 多管理员 RBAC。当前只有一个管理员，token 代其行使。
- CLI 内的 WebSocket（终端、容器 exec）。
- JSON / table 以外的输出格式。
