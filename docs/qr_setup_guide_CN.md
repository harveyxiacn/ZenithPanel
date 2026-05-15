# 扫码使用 + 正式证书申请指南

[English](qr_setup_guide.md) | 简体中文

本文是 [user_manual_CN.md](user_manual_CN.md) 的操作向延伸，覆盖装完面板
后最常做的两件事：

1. 把订阅二维码导入客户端（Clash Meta、V2RayN、Shadowrocket、NekoBox 等）。
2. 把自签名测试证书换成正式 Let's Encrypt 证书，去掉"证书不受信任"提示。

---

## 1. 把订阅扫进 Clash Meta

ZenithPanel 每个 client 都有一个订阅链接：`/api/v1/sub/<client-uuid>`。
**同一个 URL** 根据请求的 `User-Agent` 返回两种格式：

- **Clash / Clash Meta / Stash** → 完整 Clash YAML（含 fake-ip DNS、
  geoip CN 回落、AUTO 节点组）
- **V2RayN / NekoBox / Shadowrocket / 通用** → base64 编码的协议 URI
  列表（`vless://`、`vmess://`、`trojan://` 等）

### Clash Meta 手机操作步骤

1. 面板进入 **代理 → 用户与订阅**。
2. 在 client 行点右侧 **二维码** 按钮。
3. 弹窗里把格式选择器切到 **Clash / Mihomo**。
4. 手机 Clash Meta：**+ → 扫描二维码** → 对准面板屏幕。
5. **配置** 里出现新的代理组，点选即可激活。

### V2RayN / NekoBox 操作步骤

流程相同，但二维码弹窗里选 **V2Ray / V2RayN** 格式。二维码就是标准的
`vmess://` 或 `vless://` 等 URI，所有主流客户端都识别。

### 同一台设备测试多个协议

每个 client UUID 对应一条入站。要同一台手机测多个协议，扫多个 client
的二维码——每个会变成客户端里独立的代理项，自由切换。

---

## 2. 自签名证书的问题

ZenithPanel 启动测试入站时会生成一份 7 天有效的**自签名**证书，
让 TLS 类协议（VMess+WS+TLS、Trojan、Hysteria2、TUIC）能开箱跑通。
订阅链接里会带 `allowInsecure=1`，让客户端接受这种自签名证书。

**做测试可以**，但长期用有两个问题：

- 证书每 7 天过期，自签名证书面板不会自动续（只续 ACME 管的证书）。
- `allowInsecure` 名副其实——客户端会信任**任何**证书，意味着在传输
  路径上的中间人能换上自己的证书读你的流量。

只要不只想"测一下协议通不通"，就该换正式证书。

---

## 3. 申请正式 Let's Encrypt 证书

### 前置条件

- 你拥有一个域名（或子域名）。
- 该域名 A 记录指向 VPS 的公网 IP。
- 申请期间防火墙 **80/tcp 必须放行**（HTTP-01 挑战）。可执行
  `zenithctl firewall add --port 80 --proto tcp --action ACCEPT`，
  或 `ufw allow 80/tcp`，然后再点申请。

### Web UI 方式

1. **设置 → HTTPS / TLS 配置 → Let's Encrypt (ACME)**。
2. 填域名（例：`proxy.example.com`）和接收续期通知的邮箱。
3. 点 **申请证书**。内嵌的 lego 临时绑定 `:80`，完成挑战，把证书
   和私钥写到：
   - `/opt/zenithpanel/data/certs/<域名>.crt`
   - `/opt/zenithpanel/data/certs/<域名>.key`
4. 成功后绿色横幅展示两个路径和到期时间。

### CLI 方式

```bash
zenithctl cert issue --domain proxy.example.com --email you@example.com
```

输出含两个路径和 `not_after` Unix 时间戳，成功 exit code 0。

### 把证书接到入站上

ACME 只产出文件，还要让入站用上：

- **单个入站**：Web UI 编辑该入站，**stream → tlsSettings** 切手动
  JSON，把 `certificateFile` / `keyFile` 改为上一步的路径。
- **面板自身的 HTTPS**：同一个 TLS 设置面板里 **上传证书** 模块，
  从 `/opt/zenithpanel/data/certs/` 选两个文件，然后重启面板。

入站（或面板）开始用正式证书后，建议把订阅链接里的
`allowInsecure=1` 关掉——编辑入站的 stream，删掉
`allowInsecure: true`。

### 自动续期

面板内置续期 goroutine：每 12 小时扫一次 `/opt/zenithpanel/data/certs/`
下的 `.crt`，**到期前 30 天**的会用首次申请时记下的 `acme_email` 自动
重新申请。**无需手动续期**。

---

## 4. 排障

| 现象 | 通常原因 |
|---|---|
| `400 :: invalidContact :: contact email has forbidden domain` | Let's Encrypt 拒绝 `example.com` 等占位域名邮箱。用你真实的邮箱。 |
| `400 :: rejectedIdentifier :: …` | 域名还没解析到本 VPS。检查 DNS，等 5 分钟再试。 |
| `connection refused on :80` | 防火墙没开 80 端口。`ufw allow 80/tcp` 后重试。 |
| `acme: error: 429 :: urn:ietf:params:acme:error:rateLimited` | 同域名申请太频繁，参考 [Let's Encrypt 限额](https://letsencrypt.org/docs/rate-limits/) 等。 |
| 证书已发，客户端仍报错 | 确认**入站**的 `certificateFile`/`keyFile`（或 sing-box 原生 `certificate_path`/`key_path`）指向 ACME 路径，然后重新 apply。 |
| 续期了但代理没用上 | Sing-box 启动时读证书——续期后需要 `zenithctl proxy apply` 触发重载。（Xray 每次连接动态读，无需重启。） |

---

## 5. 端到端自检清单

一切就绪时你应该看到：

```bash
$ zenithctl --output table proxy status
FIELD                  VALUE
xray_running           ✓
singbox_running        ✓
dual_mode              ✓
…

$ zenithctl --output table proxy test all
ID  TAG            PROTOCOL     TRANSPORT    OK  STAGE  ELAPSED
1   …              vless        tcp+reality  ✓   -      1ms
…

$ curl -sf https://your-domain.example.com/api/v1/health
{"db":"ok","proxy":"running",…}
```

然后手机：打开 Clash Meta → 点代理组 → 选节点 → 流量走该协议 →
出口 IP 是你的 VPS 公网 IP。
