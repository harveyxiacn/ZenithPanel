# ZenithPanel 优化实施详细设计

状态：实现完成（本地验证通过；容器级门禁由 CI 执行）  
依据：2026-07-15 项目审计报告

## 1. 目标与原则

本轮优化覆盖鉴权、OTA、备份恢复、依赖供应链、SQLite、测试与 CI、
后端/前端可维护性、终端会话、文件管理器和前端性能。实施遵循：

1. 控制面默认拒绝；新增路由没有权限声明时不能被 API Token 调用。
2. 破坏性操作必须可验证、可回滚；不能用“请求已接受”代替成功。
3. 备份恢复同时恢复数据库状态和内存/进程运行态。
4. 安全门禁在 CI 中以实际生产构建工具链执行。
5. 先补高风险行为测试，再进行结构拆分，避免只移动已有缺陷。

## 2. API 授权模型

### 2.1 Principal

- 浏览器管理员 JWT、root Unix socket：通配权限 `*`。
- API Token：使用持久化的逗号分隔 scope；未知 scope 在创建时拒绝。
- `admin` 是显式超级权限，等价于全部已知 scope，但仍保留审计主体。

### 2.2 Scope

| Scope | 权限边界 |
|---|---|
| `read` | 普通面板、代理、用户、流量等只读 API |
| `write` | 入站、用户、路由、出站、部署等业务配置变更 |
| `proxy:apply` | 启停、应用和重载代理引擎 |
| `infra:read` | Docker、文件、Firewall、Cron 和系统诊断只读能力 |
| `infra:write` | Docker、文件、Firewall、Cron 和系统调优写操作 |
| `terminal` | 申请终端票据和建立 PTY 会话 |
| `admin` | Token、认证、密钥、通知、备份恢复、OTA、站点等管理能力 |

授权中间件位于 `AuthMiddleware` 之后。危险路径先按路径分类；普通路由再按
HTTP method 分类。无法分类的请求返回 403，确保新增 API 不会自动继承写权限。

### 2.3 验收

- 仅 `read` 的 token 不能访问终端、文件、Cron、Docker、Firewall、OTA。
- `write` 不隐含 `read`，调用方需要显式申请组合 scope。
- `proxy:apply` 只能执行代理生命周期 API，不能修改入站或宿主机。
- `admin` 和 `*` 可访问全部受保护路由。
- 每类 scope 均有允许/拒绝的表驱动测试。

## 3. HTTP 与会话安全

- 默认调用 `SetTrustedProxies(nil)`；仅信任 `ZENITH_TRUSTED_PROXIES` 中的
  IP/CIDR。配置非法时启动失败，不能回退为信任全部。
- Panel Server 设置 ReadHeader、Read、Write、Idle 超时及 MaxHeaderBytes。
- CORS 使用 URL 解析和精确 loopback 主机匹配，不再使用字符串前缀。
- 增加 CSP、Permissions-Policy；原生 TLS 或受信代理确认的 HTTPS 响应增加 HSTS。
- 浏览器 JWT 已迁移到 HttpOnly、SameSite=Strict Cookie；Cookie 写请求执行
  exact-origin 校验。终端改用一次性、短时、绑定来源 IP 的票据，JWT
  不再出现在 WebSocket query string。

## 4. OTA 状态机

1. 解析当前部署使用的镜像引用和更新通道。
2. 拉取候选镜像并以 RepoDigest/manifest digest 比较，而不是 image config ID。
3. 保留旧容器，停止旧容器后启动候选容器。
4. 在宿主网络轮询候选 `/api/v1/health`，校验 DB 与 HTTP 状态。
5. 健康后删除旧容器；失败则停止/删除候选、恢复旧容器名称并启动。
6. 所有阶段写入结构化更新状态，UI 可查询最终成功或回滚原因。
7. 更新镜像使用 digest 固定，并验证 CI 生成的 Sigstore 签名。

验收包括启动失败、健康超时、Docker API 失败、成功切换四条集成路径。

## 5. 备份恢复

- 将通知 token、Webhook URL 等秘密设置纳入排除表；备份响应禁止缓存。
- 设置按 `key` upsert，不使用来源数据库的主键。
- 恢复前执行格式、关系、端口冲突、Cron 表达式和 JSON 配置 dry-run。
- 数据事务成功后依次重载 scheduler、webserver、通知配置并 apply proxy。
- 任一运行态协调失败时返回“数据已恢复但运行态未协调”的明确状态，并提供
  可重试 reconcile；不能返回无条件成功。
- 增加跨数据库 ID 不一致、秘密过滤、Cron 重载和代理协调测试。

## 6. 数据库与后台任务

- SQLite 启用 WAL、foreign_keys 和 busy_timeout；生产连接池参数显式设置。
- 数据库连接池限制为单连接 writer 协调器，避免多个 goroutine 同时争用
  SQLite writer；高频 token `last_used` 写入按分钟节流。
- 增加并发写、关机 drain、WAL 恢复测试和 DB 锁指标。

## 7. 前后端结构与类型

- `router.go` 按 system、infra、proxy、security、backup、sites 拆分注册器。
- `ProxyView`、`SecurityView` 拆为领域 composable 和子组件；页面只负责布局。
- 定义统一 `ApiEnvelope<T>`，API 层不再返回 `any`；逐步由 OpenAPI 生成 DTO。
- 文件管理器以 `/home` 为虚拟根，前端不能导航到沙箱外路径。
- ECharts 与终端仅在对应页面/标签加载；移除阻碍 tree-shaking 的 chunk 配置。

## 8. CI 与供应链

- 前端单测成为必过 job；增加 Linux race 定时任务。
- 在生产 Go builder 版本上运行 `govulncheck`，Node 运行 `npm audit`，最终镜像
  再由 Trivy 扫描。
- 生成 SBOM，扫描最终镜像并验证签名；基础镜像和下载二进制使用 digest/checksum。
- 覆盖率先建立可见基线，再对 updater、scheduler、firewall、terminal、webserver
  设置逐阶段门槛。

## 9. 完成定义

只有在上述模块均有实现、自动化测试和实际构建/扫描证据，且权限矩阵、OTA
回滚、跨主机恢复三个端到端场景通过后，本优化任务才视为完成。

## 10. 实施与验证证据（2026-07-15）

- 权限矩阵、JWT purpose、可信代理、Cookie/Origin、终端票据和文件沙箱均有
  后端自动化测试；全部 `go test ./...` 通过。
- OTA 的 digest 比较、持久状态、健康超时、成功提交及回滚路径已有测试；
  更新 UI 使用有界轮询，并有成功、回滚、超时三类前端测试。
- 备份恢复执行关系和配置 dry-run、事务恢复、按 key upsert，并在事务后统一
  reconcile scheduler、webserver、notify 和 proxy。
- SQLite WAL/PRAGMA/连接池与并发重开测试通过；后端总语句覆盖率 33.4%，
  高于当前 CI 30% 基线。
- `go vet ./...`、`govulncheck ./...`（可达漏洞 0）、前端 16 项测试、
  TypeScript 生产构建和 `npm audit`（漏洞 0）通过。
- CI 已配置 Linux race、Docker 启动 smoke、SBOM、最终镜像 Trivy 门禁、
  构建 provenance 和 keyless cosign 签名；本机 Docker Desktop daemon 不可用，
  因而容器构建/启动/镜像扫描由该 CI 门禁在 Linux runner 上完成。
