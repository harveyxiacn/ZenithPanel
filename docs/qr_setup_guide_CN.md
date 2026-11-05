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

### 备选：DNS-01 via DDNS（80 端口被占用时）

面板内嵌的 lego 走 HTTP-01 挑战时需要临时绑定 `:80`。如果你的 80 端口
被别的服务长期占用（反向代理、ingress 控制器、`tunwg` 这类隧道守护
进程等），HTTP-01 会失败，又不可能每次续期都停掉它。

干净的做法是改走 **DNS-01**，通过有 API 的动态 DNS 服务商完成挑战。
acme.sh 内置支持很多服务商——例如 **DuckDNS、Cloudflare、DNSPod、
阿里云 DNS、Namecheap、He.net** 等，凡是
[acme.sh dnsapi 列表][acme-dnsapi]里的都能用。

[acme-dnsapi]: https://github.com/acmesh-official/acme.sh/wiki/dnsapi

**操作步骤**（这里以 DuckDNS 为举例，其他服务商对应的环境变量名
请参考上面 dnsapi 页面）：

```bash
# 1. 安装 acme.sh
curl https://get.acme.sh | sh -s email=you@example.com

# 2. 服务商 API 凭据（此例为 DuckDNS）。
#    用 Cloudflare 时则设置 CF_Token / CF_Account_ID，依此类推。
export DuckDNS_Token='<你的服务商 token>'

# 3. 用解析到本 VPS 的域名签发
~/.acme.sh/acme.sh --issue --dns dns_duckdns \
  -d your-subdomain.duckdns.org \
  --server letsencrypt

# 4. 安装到面板证书目录，并设置续期后自动 reload sing-box。
#    Xray 每次连接动态读取证书，sing-box 启动时一次性读，
#    所以续期后必须 proxy apply 才会让新证书生效。
~/.acme.sh/acme.sh --install-cert -d your-subdomain.duckdns.org --ecc \
  --fullchain-file /opt/zenithpanel/data/certs/fullchain.pem \
  --key-file       /opt/zenithpanel/data/certs/privkey.pem \
  --reloadcmd      'docker exec zenithpanel /opt/zenithpanel/zenithpanel ctl proxy apply >/dev/null 2>&1'
```

acme.sh 在安装时自动创建 cron，到期前约 60 天会自动重新签发；
`--reloadcmd` 保证 sing-box 加载新证书。token 持久化在
`~/.acme.sh/account.conf`（权限 600）。

#### 可选：让 DDNS 域名跟着 IP 变更自动更新

如果你的 VPS 公网 IP 可能变（重装、迁移、ISP 轮换），加一行 cron
让 DDNS 记录跟随主机。DuckDNS 为例：

```bash
( crontab -l 2>/dev/null; echo "*/5 * * * * curl -fsS 'https://www.duckdns.org/update?domains=your-subdomain&token=<TOKEN>' >/dev/null" ) | crontab -
```

其他服务商有对应的更新 URL 或 CLI（如 `cloudflare-ddns`、
`cf-ddns.sh`），按你用的服务商挑一个。

> **为什么不直接默认 Cloudflare / DNSPod？** 这些其实和上面一样
> 跑得通——acme.sh 的 `--dns dns_cf`、`--dns dns_dp` 是
> `dns_duckdns` 的等价替换。这里用 DuckDNS 举例只是因为它免费、
> 注册即用，你按自己情况换就行。

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

## 5. 修改入站端口

入站的监听端口随时可改，**升级面板后仍然保留**——因为入站表存放在 SQLite，
DB 文件 (`/opt/zenithpanel/data/zenith.db`) 在 Docker 卷里持久挂载；
OTA 升级时 panel 读出旧容器的 `HostConfig` 原样传给新容器，所以这个卷以及
里面的所有数据（入站、证书、审计日志、设置）都不会丢。

### Web UI

1. **代理 → 入站节点**，点行尾的 **编辑**。
2. 改 **端口** 字段，保存。
3. **应用配置** 让引擎在新端口上重新绑定。
4. **在防火墙放行新端口**。**设置 → 防火墙** 里可以加规则，或者宿主机直接
   `ufw allow N/tcp`。

### CLI

```bash
zenithctl inbound set-port 3 31402                 # 仅改端口
zenithctl inbound set-port 3 31402 --sync-firewall # 同时 UFW 放行新端口
```

CLI 会显示旧端口与新端口，自动跑 `proxy apply`，并在 `--sync-firewall` 模式
下通过现有防火墙 API 加规则。旧端口仍留在 UFW 里——确认没有其它入站还在用
它之后，用 `ufw delete allow <旧>/tcp` 手动清掉。

### OTA 升级保留的内容

- **入站端口**：在数据卷里的 SQLite 中
- **API token、审计日志、设置、证书**：同一个数据卷
- **UFW 规则**：宿主机层级，不在容器里，不会动
- **镜像内置默认 (Dockerfile `CMD`)**：会被新镜像替换

所以你在面板里把 443 改成 8443，明天 OTA 升级，新容器仍然绑 8443。无需二次
迁移。

---

## 6. 端到端自检清单

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
