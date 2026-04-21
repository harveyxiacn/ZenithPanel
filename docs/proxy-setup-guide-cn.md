# V2Ray / Xray 代理设置指南

本指南将帮助你在 ZenithPanel 中设置代理节点，并将其导入到客户端应用（Clash、V2RayN 等）。

---

## 🚀 推荐：使用智能部署 (Smart Deploy)

如果你的目标是 **稳定出口 IP、规避平台风控**（银行、交易所、电商账号的地理/IP 画像），**不需要**手动一个个配置节点。使用智能部署一键完成：

1. 登录面板后访问 `/smart-deploy`
2. 第 1 步：面板自动探测环境（内核、BBR、端口、时间同步、防火墙……）
3. 第 2 步：从 4 个预设中选择——**"稳定出口"是默认推荐**
4. 第 3 步：（可选）填写域名 / Reality SNI / 自定义端口
5. 第 4 步：预览将要执行的所有操作（协议、调优、证书、防火墙），一眼看清
6. 第 5 步：确认执行，约 30 秒后生成订阅链接

每一步都有回滚：如果中途失败或你改主意，"回滚"一键还原全部系统改动（sysctl、systemd drop-in、创建的入站节点）。

### 四个预设怎么选

| 预设 | 协议 | 适用场景 |
|---|---|---|
| **稳定出口** ⭐ | VLESS + Reality · TCP 443 | 金融/电商账号固定 IP，规避风控；无需域名 |
| **速度优先** | Hysteria2 · UDP 443 | 低延迟高吞吐；建议配域名用 ACME 证书 |
| **全能组合** | Reality (TCP) + Hysteria2 (UDP) | 想要 TCP/UDP 双入口，客户端按需切换 |
| **移动弱网** | Hysteria2 + TUIC · 双 UDP 端口 | 手机 4G/5G、丢包较高的网络 |

### 为什么"稳定出口"能规避风控

平台的风控引擎看的是：IP 是否稳定、TLS 指纹是否像真实浏览器、是否有数据中心出口的典型特征。智能部署的"稳定出口"预设：

- **单 IP 单端口**：只有你的 VPS 一个出口，不会像商用代理那样 IP 轮换
- **Reality 协议**：握手时伪装成你指定的真实网站（默认 `www.microsoft.com`），TLS 指纹与 Chrome 一致
- **TCP 443**：最普适的端口，避免银行 App 阻断 UDP
- **VPS IP 较数据中心**：比共享住宅代理更稳定，比商用 IP 池更干净

---

## 手动配置（适合已有经验的用户）

如果你想精细控制每个节点的参数、需要特殊的路由规则，或者智能部署的预设不覆盖你的用例——继续往下看。

---

## 前提条件

- ZenithPanel 已部署并运行（参见 `development_guide_CN.md`）
- 一台拥有公网 IP 的 VPS
- （可选）一个已解析到 VPS 的域名，用于 TLS 证书

## Docker 启动命令

确保容器使用以下参数启动：

```bash
docker run -d \
  --name zenithpanel \
  --network host \
  --pid=host \
  --privileged \
  --restart unless-stopped \
  -v zenith-data:/opt/zenithpanel/data \
  -v /var/run/docker.sock:/var/run/docker.sock \
  ghcr.io/harveyxiacn/zenithpanel:main
```

> `--network host` 是必需的，这样 Xray 才能直接监听 VPS 的端口。

---

## 第一步：创建入站节点

### 方式 A：快速配置（推荐）

最简单的方式——一键自动配置：

1. 进入 **代理服务（Proxy Services）** > **入站节点（Inbound Nodes）** 标签页。
2. 点击 **Quick Setup**（节点列表为空时也会显示快速配置入口）。
3. **选择方案**：从 6 种预设中选择。点击 **Use Recommended** 一键选择 VLESS+Reality（抗封锁最强，无需域名）。
4. **检查配置**：所有设置已自动填充：
   - Reality 密钥（X25519）和 Short ID 由服务端自动生成
   - WebSocket 路径随机生成
   - Shadowsocks 密码自动生成
   - 展开任意节点可自定义端口、域名、证书路径等
5. 勾选 **Add recommended routing rules** 自动创建广告屏蔽和私有 IP 拦截规则。
6. 勾选 **Create first client** 自动创建首个用户（含订阅链接）。
7. 点击 **Create All** — 完成！

| 方案 | 默认端口 | 是否需要域名 | 引擎 | 备注 |
|------|---------|------------|------|------|
| VLESS + Reality | 443 | 否 | Xray / Sing-box | 抗封锁能力最强 |
| VLESS + WS + TLS | 2083 | 是 | Xray / Sing-box | 支持 CDN（Cloudflare）转发 |
| VMess + WS + TLS | 2087 | 是 | Xray / Sing-box | 客户端兼容性最广 |
| Trojan + TLS | 2096 | 是 | Xray / Sing-box | 简单高速 |
| Hysteria2 | 8443 | 是 | **仅 Sing-box** | UDP/QUIC 超高速 |
| Shadowsocks | 8388 | 否 | Xray / Sing-box | 轻量级 |

> **重要提示**：Hysteria2 仅支持 Sing-box 引擎。如果使用 Xray 引擎，Hysteria2 入站节点会被自动跳过并显示警告。通过 **Apply** 下拉菜单切换到 Sing-box 引擎以使用 Hysteria2。

### 方式 B：手动配置（高级用户）

如需完全自定义，点击 **Add Node** 手动配置：

### 示例：VLESS + TCP + TLS

| 字段     | 值 |
|----------|-------|
| Tag      | `vless-tcp-tls` |
| Protocol | `vless` |
| Listen   | `0.0.0.0` |
| Port     | `443` |

**Settings JSON**（协议配置）：
```json
{
  "decryption": "none",
  "flow": "xtls-rprx-vision"
}
```

**Stream JSON**（传输层 + TLS）：
```json
{
  "network": "tcp",
  "security": "tls",
  "tlsSettings": {
    "serverName": "你的域名.com",
    "certificates": [
      {
        "certificateFile": "/etc/letsencrypt/live/你的域名.com/fullchain.pem",
        "keyFile": "/etc/letsencrypt/live/你的域名.com/privkey.pem"
      }
    ]
  }
}
```

### 示例：VLESS + Reality（无需域名）

| 字段     | 值 |
|----------|-------|
| Tag      | `vless-reality` |
| Protocol | `vless` |
| Port     | `443` |

**Settings JSON：**
```json
{
  "decryption": "none",
  "flow": "xtls-rprx-vision"
}
```

**Stream JSON：**
```json
{
  "network": "tcp",
  "security": "reality",
  "realitySettings": {
    "dest": "www.microsoft.com:443",
    "serverNames": ["www.microsoft.com"],
    "publicKey": "你的公钥",
    "privateKey": "你的私钥",
    "shortIds": ["abcd1234"]
  }
}
```

> **提示**：快速配置会自动生成 Reality 密钥。手动配置时可通过命令生成：`xray x25519`
> 在容器中执行：`docker exec zenithpanel xray x25519`
> 或调用面板 API：`POST /api/v1/proxy/generate-reality-keys`

### 示例：VMess + WebSocket + TLS

| 字段     | 值 |
|----------|-------|
| Tag      | `vmess-ws-tls` |
| Protocol | `vmess` |
| Port     | `443` |

**Settings JSON：**
```json
{}
```

**Stream JSON：**
```json
{
  "network": "ws",
  "security": "tls",
  "wsSettings": {
    "path": "/vmws",
    "headers": {
      "Host": "你的域名.com"
    }
  },
  "tlsSettings": {
    "serverName": "你的域名.com",
    "certificates": [
      {
        "certificateFile": "/etc/letsencrypt/live/你的域名.com/fullchain.pem",
        "keyFile": "/etc/letsencrypt/live/你的域名.com/privkey.pem"
      }
    ]
  }
}
```

### 示例：Trojan + TCP + TLS

| 字段     | 值 |
|----------|-------|
| Tag      | `trojan-tls` |
| Protocol | `trojan` |
| Port     | `443` |

**Settings JSON：**
```json
{}
```

**Stream JSON：**
```json
{
  "network": "tcp",
  "security": "tls",
  "tlsSettings": {
    "serverName": "你的域名.com",
    "certificates": [
      {
        "certificateFile": "/etc/letsencrypt/live/你的域名.com/fullchain.pem",
        "keyFile": "/etc/letsencrypt/live/你的域名.com/privkey.pem"
      }
    ]
  }
}
```

### 示例：Shadowsocks

| 字段     | 值 |
|----------|-------|
| Tag      | `ss-aead` |
| Protocol | `shadowsocks` |
| Port     | `8388` |

**Settings JSON：**
```json
{
  "method": "2022-blake3-aes-128-gcm",
  "password": "你的服务器密码",
  "network": "tcp,udp"
}
```

---

## 第二步：应用配置

创建入站节点后，点击代理服务页面顶部的 **Apply Configuration** 按钮。这将生成配置文件并启动/重启代理引擎。

### 引擎选择

ZenithPanel 支持两种代理引擎：

| 引擎 | API 命令 | 支持协议 |
|------|---------|---------|
| **Xray**（默认） | `POST /api/v1/proxy/apply?engine=xray` | VLESS, VMess, Trojan, Shadowsocks |
| **Sing-box** | `POST /api/v1/proxy/apply?engine=singbox` | 以上所有 + **Hysteria2** |

如果你的入站节点包含 Hysteria2，**必须**使用 Sing-box 引擎。Xray 会自动跳过不支持的协议并显示警告。

### 崩溃检测

如果代理引擎启动后立即崩溃（配置错误、端口冲突、缺少二进制文件），API 会返回包含 stderr 输出的错误消息。状态接口也会包含 `xray_last_error` / `singbox_last_error` 字段用于排查。

你可以通过 API 查看运行状态、触发应用或预览生成配置：
```
GET /api/v1/proxy/status
POST /api/v1/proxy/apply?engine=xray
POST /api/v1/proxy/apply?engine=singbox
GET /api/v1/proxy/config/xray
GET /api/v1/proxy/config/singbox
```

---

## 第三步：创建用户（Client）

进入 **用户与订阅（Users & Subs）** 标签页，点击 **Add Client**。

| 字段          | 值 |
|---------------|-------|
| Email         | `user1@example.com` |
| Select Inbound| 选择第一步创建的入站节点 |
| Traffic Limit | `0`（无限制）或总字节数（如 `107374182400` = 100GB） |

UUID 会自动生成。创建后：
- 点击 **Sub Link** 打开格式选择器，并复制明确格式的订阅链接。
- 点击 **QR Code** 生成手机客户端可扫描的二维码（支持 V2Ray/Base64 和 Clash/YAML 两种格式）。

如果你是通过管理域名、反向代理或隧道访问面板，而真实代理节点需要使用另一个公网地址，请先编辑入站并填写 **公网主机 / IP**，再导入订阅。这个字段决定了 Clash 和 V2Ray 客户端配置里实际拨号的目标地址。

---

## 第四步：导入到客户端应用

### 订阅链接格式
```
https://你的服务器:面板端口/api/v1/sub/用户UUID
```

面板会根据 `User-Agent` 请求头自动判断客户端类型：
- **Clash/Mihomo/Stash/Surge/Shadowrocket/Loon** -> Clash YAML 格式
- **V2RayN/V2RayNG/其他** -> Base64 编码链接

> 订阅请求里的主机名只会作为兜底值使用。如果你的代理节点应该使用不同的公网域名或 IP，请在入站里设置 **公网主机 / IP**，确保生成的订阅把客户端指向正确的出口地址。

你也可以手动指定格式：
```
https://服务器:端口/api/v1/sub/UUID?format=clash
https://服务器:端口/api/v1/sub/UUID?format=base64
```

### Clash / Mihomo
1. 打开 Clash -> **配置（Profiles）**
2. 点击 **导入（Import）** 或粘贴订阅链接
3. 点击 **更新（Update）** 下载配置
4. 选择 **PROXY** 分组，选择一个节点

### V2RayN（Windows）
1. 打开 V2RayN -> **订阅（Subscription）** -> **订阅设置**
2. 添加新订阅，粘贴 ZenithPanel 提供的明确 Base64 链接：
   `https://服务器:端口/api/v1/sub/UUID?format=base64`
3. 点击 **更新订阅** -> 节点会出现在列表中
4. 右键节点 -> **设为活动服务器**

### V2RayNG（Android）
1. 打开 V2RayNG -> 点击 **+** -> **从URL导入配置**
2. 粘贴 ZenithPanel 提供的明确 Base64 链接：
   `https://服务器:端口/api/v1/sub/UUID?format=base64`
3. 点击播放按钮连接

### Shadowrocket（iOS）
1. 打开 Shadowrocket -> 点击右上角 **+**
2. 选择 **Subscribe** -> 粘贴链接
3. 点击更新，然后选择节点

---

## 第五步：开放防火墙端口

确保入站节点的端口已开放。可以使用 ZenithPanel 内置的防火墙页面，或通过终端操作：

```bash
# 示例：开放 TCP 443 端口
iptables -I INPUT -p tcp --dport 443 -j ACCEPT

# 示例：开放 TCP+UDP 8388 端口
iptables -I INPUT -p tcp --dport 8388 -j ACCEPT
iptables -I INPUT -p udp --dport 8388 -j ACCEPT
```

---

## 路由规则

进入 **代理服务** > **路由规则（Routing Rules）** 标签页，添加规则来控制流量走向：

| Outbound Tag | 用途 |
|-------------|---------|
| `direct`    | 直连（不经过代理） |
| `block`     | 拦截（丢弃流量） |

示例：屏蔽广告域名：
- Domain: `geosite:category-ads-all`
- Outbound Tag: `block`

示例：中国 IP 直连：
- IP: `geoip:cn`
- Outbound Tag: `direct`

---

## 常见问题

**Xray/Sing-box 启动失败：**
- Apply 按钮现在会显示引擎崩溃时的具体错误信息
- 检查状态 API：`GET /api/v1/proxy/status` — 查看 `xray_last_error` 或 `singbox_last_error`
- 检查端口是否被占用：`netstat -tlnp | grep 443`
- 预览生成的配置：`GET /api/v1/proxy/config/xray`
- 手动运行排查：`xray run -c /opt/zenithpanel/data/xray_config.json`

**Xray 使用 Hysteria2 时持续崩溃：**
- Hysteria2 **不支持 Xray** 引擎。请切换到 Sing-box：`POST /api/v1/proxy/apply?engine=singbox`
- Xray 会自动跳过 Hysteria2 入站节点并显示警告

**TLS 证书错误：**
- 确保证书文件在容器内可访问。如果使用宿主机的 Let's Encrypt 证书，挂载证书目录：
  ```bash
  -v /etc/letsencrypt:/etc/letsencrypt:ro
  ```

**客户端无法连接（直连正常但代理不通）：**
- 确认 VPS 防火墙端口已开放
- 确认入站节点已启用且引擎正在运行（检查状态）
- 确认用户已启用且未过期
- Reality 节点：确保密钥已生成（快速配置会自动生成）
- 检查订阅链接返回的配置是否正确：`curl -v https://服务器:端口/api/v1/sub/UUID`
- Clash 订阅会自动添加服务器地址的直连规则，防止代理回环

**生成 Reality 密钥对：**
快速配置会自动生成密钥。手动配置时：
```bash
docker exec zenithpanel xray x25519
```
或调用 API：`POST /api/v1/proxy/generate-reality-keys` — 返回 `private_key`、`public_key` 和 `short_id`。

将私钥填入 Stream JSON 的 `realitySettings.privateKey`，公钥填入 `realitySettings.publicKey`（公钥会通过订阅链接下发给客户端）。
