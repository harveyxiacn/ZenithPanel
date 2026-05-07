# 协议与引擎使用指南

## 各协议引擎支持情况

| 协议 | Xray | Sing-box | 备注 |
|------|------|----------|------|
| VLESS | ✅ | ✅ | 两个引擎均支持；XTLS Vision flow 仅 Xray 支持 |
| VMess | ✅ | ✅ | 两个引擎均支持 |
| Trojan | ✅ | ✅ | **必须配置 TLS 或 Reality**，否则 Sing-box 将拒绝启动 |
| Shadowsocks | ✅ | ✅ | AEAD-2022 方法支持按用户统计流量（见下文） |
| Hysteria2 | ❌ | ✅ | **仅 Sing-box 支持**，请切换至 Sing-box 引擎 |
| TUIC v5 | ❌ | ✅ | **仅 Sing-box 支持**，请切换至 Sing-box 引擎 |
| WireGuard | ❌ | ❌ | 计划中，暂未实现 |

## 引擎互斥说明

Xray 与 Sing-box **不能同时运行**，因为它们会争抢相同的 inbound 端口。
若两个引擎同时启动，后启动的引擎会因 `address already in use` 而启动失败。

**「应用配置」按钮已实现引擎互斥逻辑**：

- 选择 **Xray**：先停止 Sing-box，再启动 Xray。
- 选择 **Sing-box**：先停止 Xray，再启动 Sing-box。

当 inbound 列表中存在 Hysteria2 或 TUIC 时，UI 引擎选择器会自动切换至 Sing-box。

## Shadowsocks 多用户（AEAD-2022）

传统 Shadowsocks 方法（aes-256-gcm、chacha20-poly1305 等）使用同一个共享密码，
流量统计仅能精确到 inbound 级别，无法区分用户。

AEAD-2022 方法支持按用户模式：
- `2022-blake3-aes-128-gcm`
- `2022-blake3-aes-256-gcm`
- `2022-blake3-chacha20-poly1305`

配置上述方法后，每个 Client 的 UUID 将作为其独立密码注入配置，从而实现：
- 按用户统计上传/下载流量
- 单独封禁某个用户而不影响其他用户

**配置方法**：在 inbound 的 settings JSON 中将 `method` 设为 AEAD-2022 值，
订阅链接仍使用 SIP002 格式，客户端以 UUID 作为密码连接。

## Trojan 必须配置 TLS

Trojan 协议在 Sing-box 中**强制要求 TLS 或 Reality**，否则：

- 在创建/更新 inbound 时，后端会直接返回校验错误（`validateInbound`）。
- 若已有未配置 TLS 的 Trojan inbound，Sing-box 将无法启动（整个进程退出）。

请确保 inbound 的 stream JSON 中设置 `"security": "tls"` 或 `"security": "reality"`。
TLS 模式下还需在 `tlsSettings.certificates` 中指定有效的证书和私钥路径。

## ACME / Let's Encrypt 证书

`POST /api/v1/proxy/tls/issue` 现已通过 lego 实现真正的 ACME HTTP-01 挑战。

**前置条件**：
- 域名 A 记录指向本服务器公网 IP。
- 服务器的**80 端口必须对外开放**（HTTP-01 挑战需要）。
- 提供有效的 `email` 用于 ACME 账号注册。

证书存储路径：`/opt/zenithpanel/data/certs/<domain>.{crt,key}`，权限 `0600`。

Smart Deploy 中选择 `cert_mode=acme` 时，会自动触发此流程。

## 订阅链接

`subscription-userinfo` 响应头现已加入 `expire=<unix时间戳>` 字段
（当客户端设置了到期时间时）。兼容 Clash Meta、v2rayN 6+ 等客户端，
这些客户端会在订阅列表中直接显示到期时间。
