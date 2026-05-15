# ZenithPanel — AI Agent 操作手册

[English](AGENTS.md) | 简体中文

本文是给 **AI agent（Claude Code、Cursor、Aider 等）和自动化系统**
端到端驱动一个 ZenithPanel 实例的操作手册。假设你能 SSH 到目标 VPS，
或持有 API token 走 HTTPS。

人面向文档：
- [docs/user_manual_CN.md](docs/user_manual_CN.md) — 运维人员手册
- [docs/qr_setup_guide_CN.md](docs/qr_setup_guide_CN.md) — 扫码 + 证书完整指引
- [docs/cli_design_CN.md](docs/cli_design_CN.md) — `zenithctl` 命令树
- [docs/cli_api_spec.md](docs/cli_api_spec.md) — 每个 HTTP 端点的契约（仅 EN）

---

## 1. 三条认证路径，选一条

| 路径 | 用于 | 鉴权 | 注意 |
|---|---|---|---|
| **Unix socket** (`/run/zenithpanel.sock`) | SSH 进 VPS、root 身份、面板本机 | 无（FS 权限托底） | 仅 Linux；只有 root 能 connect |
| **HTTPS + API token** | 远程，或不愿 docker exec | `Authorization: Bearer ztk_…` | 用一次 `zenithctl token bootstrap` 自助创建；持久化在 `~/.config/zenithctl/config.toml` |
| **JWT** | 模拟浏览器会话 | `POST /api/v1/login` 拿 JWT | 24h 过期；不建议无人值守 |

**Agent 推荐**：首次 SSH 进 host 时跑一次 `zenithctl token bootstrap`，
之后每条命令都自动认证。跨主机（CI）把 token 写进 `ZENITHCTL_TOKEN`
+ `ZENITHCTL_HOST` 环境变量。

---

## 2. 60 秒自检

干任何事之前先跑这四条。任意一条不通就先修，别盲撞。

```bash
curl -sf http://127.0.0.1:31310/api/v1/health | jq .
zenithctl status
zenithctl --output table proxy status
zenithctl --output table proxy test all
```

- `proxy test all` 某行 stage=`not_bound` → 引擎没在那个端口上，多半是配置生成出错
- stage=`tcp` → 内核拒了（防火墙没开？）
- stage=`tls` → 证书/私钥对不上，看 §6

---

## 3. 全新 VPS 安装

```bash
docker run -d \
  --name zenithpanel \
  --restart always \
  --network host \
  -v /opt/zenithpanel/data:/opt/zenithpanel/data \
  -v /var/run/docker.sock:/var/run/docker.sock \
  ghcr.io/harveyxiacn/zenithpanel:main

# 看 setup 向导 URL
docker logs zenithpanel 2>&1 | grep -E 'one-time password|setup-'
```

Setup 向导地址 `http://<vps>:31310/zenith-setup-<随机后缀>`，访问一次走完
表单。然后给 CLI 拿 token：

```bash
ln -sf /opt/zenithpanel/zenithpanel /usr/local/bin/zenithctl
docker exec zenithpanel /opt/zenithpanel/zenithpanel ctl token bootstrap
```

**volume 挂载是契约**：缺了 `-v /opt/zenithpanel/data:...` OTA 升级就丢
入站、证书、审计日志。安装文档默认带这一行，agent 也必须保证它存在。

---

## 4. 通过 API 建协议 + 用户

通用配方：**inbound → client → apply → cert（可选）**。

### 4.1 VLESS+Reality（不需要证书）

```bash
KEYS=$(zenithctl -q raw POST /api/v1/proxy/generate-reality-keys)
PK=$(jq -r '.data.private_key' <<<"$KEYS")
PUB=$(jq -r '.data.public_key' <<<"$KEYS")
SID=$(jq -r '.data.short_id' <<<"$KEYS")

zenithctl raw POST /api/v1/inbounds --data "$(jq -nc \
  --arg ip "$PUBLIC_IP" --arg pk "$PK" --arg pub "$PUB" --arg sid "$SID" '{
  tag:"vless-reality", protocol:"vless", port:443, network:"tcp",
  server_address:$ip,
  settings:({decryption:"none",flow:"xtls-rprx-vision"}|tostring),
  stream:({network:"tcp",security:"reality",realitySettings:{
    target:"www.microsoft.com:443",
    serverNames:["www.microsoft.com"],
    privateKey:$pk,shortIds:[$sid],
    settings:{publicKey:$pub,fingerprint:"chrome"}
  }}|tostring),
  enable:true}')"

INBOUND_ID=$(zenithctl -q raw GET /api/v1/inbounds | jq '[.[]|select(.tag=="vless-reality")][0].id')
zenithctl client add --inbound "$INBOUND_ID" --email user1
zenithctl raw POST /api/v1/firewall/rules --data '{"port":"443","protocol":"tcp","action":"ACCEPT"}'
zenithctl proxy apply
```

### 4.2 一键全部 6 个协议 + 每个一个用户

用户说"帮我把全部协议都开起来"时，跑这个：

```bash
# 前置（确保都已 export）：
export PUBLIC_IP=<vps ip>
export DOMAIN=<你的域名 或者 <dashed-ip>.nip.io>
zenithctl cert issue --domain "$DOMAIN" --email you@example.com
export CERT=/opt/zenithpanel/data/certs/$DOMAIN.crt
export KEY=/opt/zenithpanel/data/certs/$DOMAIN.key

# 然后：
bash scripts/agent_seed_all_protocols.sh
```

脚本是幂等的——重复跑不会重复建。每一步会打印 `[ok] created` /
`[skip] already exists` / `[warn] ...`。最后会自动 `proxy apply` 并跑
一次 `proxy test all` 验证，6/6 OK 才算成功。

### 4.3 其余协议单建（VMess/Trojan/SS/Hy2/TUIC）

完整 JSON 模板见 [AGENTS.md §4.4](AGENTS.md#44-one-click-recipes-agent-should-map-user-request--snippet)
（recipes B–F），CN 这里不再复制——结构与上面 VLESS 一致：先 POST
`/api/v1/inbounds`，再 `client add`，再 firewall + apply。

### 4.4 协议引擎归属（默认双引擎）

| 协议 | 引擎 |
|---|---|
| VLESS / VMess / Trojan / Shadowsocks | Xray |
| Hysteria2 / TUIC | Sing-box |

`POST /proxy/apply` 不带 `engine=` 时自动分区。双引擎并行运行，绑定不
同端口。诊断时用 `?engine=xray` 或 `?engine=singbox` 强制单引擎。

### 4.5 协议侧的几个坑（实际撞过的）

- **TUIC password = UUID**。订阅 URL 发的是 `UUID:UUID`，server 也必须
  匹配——别用 `settings.clients[].password` 想给 TUIC 用户独立密码，
  会客户端认证失败静默。
- **SS-2022 多用户密码是 `serverPSK:userPSK`**（冒号拼接）。面板的 sub
  生成器已经这么做，自己拼 URL 时记得。
- **Hy2/TUIC 必须 `alpn:["h3"]`**。面板默认会补，但你自己 POST inbound
  时漏了，客户端会 `tls: server did not select an ALPN`。
- **`geoip:private`** 在路由规则里 SagerNet 不发布 `.srs`。面板已自动
  改写为 `ip_is_private:true`。

---

## 5. 改入站端口

```bash
zenithctl inbound set-port <id> <new-port> --sync-firewall
# 自动 GET → PUT → proxy apply，可选项 UFW 放行新端口
# 旧端口留在 UFW；确认无其它入站使用后手动删：
ufw delete allow <旧>/tcp
```

端口唯一性已在 server 端校验（同时段两条 enabled 入站不能撞同端口），
撞了会返回 CLI exit code 2。

---

## 6. ACME / Let's Encrypt 证书

### 前置

- 域名（自有或 `<dashed-ip>.nip.io` / `.sslip.io`）解析到 VPS 公网 IP
- 防火墙 **80/tcp** 公网可达
- 面板内置 webserver 默认不占 80（修复后），但有 Sites 时会占——`cert`
  包里的 `PortBouncer` 钩子会自动让位

### 一键签发

```bash
zenithctl cert issue --domain panel.example.com --email you@example.com
# 成功返回 cert_path / key_path / not_after Unix 时间戳
```

证书落 `/opt/zenithpanel/data/certs/<域名>.{crt,key}`。`acme_email` 被
持久化，后台**每 12h 跑一次续期 ticker**，30 天内到期的自动重签。

### 把证书接到入站

编辑入站，把 `tlsSettings.certificates[0].certificateFile` 与 `keyFile`
改为新路径，删掉 `allowInsecure`。`scripts/rabisu_switch_to_real_cert.py`
是参考实现，一次性把所有 TLS 入站迁过去。

---

## 7. OTA 升级保留 state

`POST /api/v1/system/update/apply`（或 Settings → 面板更新）走的 updater
会**原样复用旧容器的 `HostConfig`**——含 Binds（volume 挂载）、Env、
NetworkMode。所以：

| State | OTA 后保留？ |
|---|---|
| 入站端口/协议/设置/客户端 | ✅ |
| API tokens / 审计日志 / 路由规则 / AdBlock 开关 | ✅ |
| ACME 证书 + 续期用的 `acme_email` | ✅ |
| 宿主机 UFW 规则 | ✅（host 层级，不在容器） |
| Docker 镜像内置 CMD/Entrypoint | ❌——换镜像才有意义 |

Web UI/CLI 改的端口在 v2 容器里依然是那个端口，无需二次迁移。**唯一**
会出问题的：用户当初部署没挂 `-v /opt/zenithpanel/data:...`——这种属
于跑容器就该丢数据，不是 OTA 的锅。

---

## 8. 可观测性（无人值守必备）

```bash
# 健康（无需认证）
curl -sf http://<vps>:31310/api/v1/health | jq .

# Prometheus 指标（需 token）
curl -sH "Authorization: Bearer $TOKEN" http://<vps>:31310/api/v1/metrics

# 审计日志
zenithctl raw GET /api/v1/admin/audit-log
```

`zenithpanel_xray_running` 或 `_singbox_running` 为 0 → 引擎挂了，告警。
`rate(zenithpanel_client_traffic_bytes[5m])` → 用户带宽。

---

## 9. 排障决策树

1. CLI 401 → token 被吊销/过期，跑 `token bootstrap`
2. `proxy apply` 500 → `proxy status` 里看 `xray_last_error` /
   `singbox_last_error`，再 `docker logs zenithpanel | tail -50`
3. sing-box `unexpected status: 404 Not Found` 启动失败 → 路由规则用
   了 SagerNet 仓库不发布的 geosite/geoip tag（如 `geoip:private`
   要走 `ip_is_private` 不是 rule-set）
4. 证书续期 `429 rateLimited` → Let's Encrypt 周限额，换个 nip.io 子域
5. `proxy test` `not_bound` → 引擎根本没起，看 docker logs
6. 客户端连上但流量不动 → 多半 AdBlock 把目标域名带塞了，先关 AdBlock
   重试
7. OTA 升级后端口回滚 → 当初部署没挂 volume，数据丢了；从
   `zenithctl backup export` 备份恢复

---

## 10. Agent 自律

- **别 `docker rm zenithpanel`** 不先 `zenithctl backup export` 备份
- **别关 `Block Private IP` 路由规则**（默认 id=2）——防止外部用户探
  内网
- **别绕过 `validateInbound`** 端口唯一性校验直接写 DB
- **别用 `example.com` 这种保留邮箱**申请 Let's Encrypt——会撞 5/周
  限流
- **token 别写进版本控制的代码或 env 文件**——用
  `~/.config/zenithctl/config.toml`（0600）或 secret 管理器

---

## 11. 常用一行命令

```bash
# 面板全景快照
zenithctl --output table proxy status
zenithctl --output table inbound list
zenithctl --output table client list
zenithctl --output table token list
zenithctl --output table proxy test all
curl -sf http://127.0.0.1:31310/api/v1/health | jq .

# 给 CI 签一个 90 天作用域受限的 token
zenithctl token create --name ci-2026-q2 --scopes 'read,write,proxy:apply' --expires-in 90

# 在 VPS 上端到端测全协议（真实 curl 视角）
bash scripts/proto_sweep_dual.sh

# 危险：清空 + 重新 seed（仅保留证书）
zenithctl backup export --out /tmp/before.zip
zenithctl backup restore --file /path/to/fresh-seed.zip
```

---

## 12. 入门阅读顺序

1. 本文
2. [`docs/cli_design_CN.md`](docs/cli_design_CN.md) — CLI 命令树
3. [`docs/cli_api_spec.md`](docs/cli_api_spec.md) — 端点契约（EN）
4. [`docs/qr_setup_guide_CN.md`](docs/qr_setup_guide_CN.md) — 给用户做的实操流程
5. [`docs/protocol_connectivity_report.md`](docs/protocol_connectivity_report.md)
   — "通了" 长什么样

跟随 [`CHANGELOG.md`](CHANGELOG.md) 关注新端点。

---

## 13. 参考脚本

| 脚本 | 用途 |
|---|---|
| `scripts/agent_seed_all_protocols.sh` | 一键 seed 6 个协议 + 每个一个客户端 + UFW + apply + 自检。幂等。 |
| `scripts/rabisu_fix_inbounds.py` | 参考：批量把入站的 `server_address` 和 `allowInsecure` 改正确 |
| `scripts/rabisu_switch_to_real_cert.py` | 参考：批量把 TLS 入站从自签换成 ACME 真证书 |
| `scripts/proto_sweep_dual.sh` | 端到端真实 curl 测每个协议，6/6 PASS 才合格 |

`agent_seed_all_protocols.sh` 里的 `post_inbound` / `add_client` /
`open_fw` 三个 helper 是写新配方的模板。
