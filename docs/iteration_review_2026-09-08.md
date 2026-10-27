# ZenithPanel 迭代评估（2026-09-08）

## 结论与范围

项目具备继续迭代的价值。下一轮应优先提升代理服务连续性、计费正确性、低内存运行能力和发布可信度，再完善统一订阅与日常操作体验。

本评估基于当前工作区，包含已有的未提交修改，不代表这些修改已经发布到 VPS。检查覆盖后端认证、代理生命周期、流量采集、部署、OTA、CI，以及前端关键操作。未修改业务代码，也未操作生产环境。

现有基础已包括双引擎、CLI/API Token、TOTP、IP 白名单、WARP 出站、流量重置、指标、备份恢复、智能部署和多语言。当前工作区还已加入 Cookie 会话、scope 授权、终端票据、OTA 签名与回滚状态、SQLite PRAGMA 和部分组件拆分。因此，不建议再将这些能力整体列为“尚未实现”。

优先级：P1 为建议在下一次功能发布前处理的正确性/可靠性问题；P2 为随后完成的工程或体验改进。以下结论以代码路径为依据；涉及真实负载、Linux 进程与浏览器布局的表现，需要对应环境验收。

## P1-1：应用配置的前端入口与双引擎策略不一致

证据：

- `frontend/src/views/ProxyView.vue:45` 只接受 `xray | singbox`，`:165` 将选择值传入 Apply。
- `frontend/src/api/proxy.ts:80` 默认 `xray`，总是附加 `?engine=`。
- `backend/internal/api/proxy_routes.go:78` 的默认 `auto` 才进行双引擎分区；`:153` 和 `:181` 的显式单引擎分支会停止另一引擎。

影响：一个已正常运行的混合部署，在用户选择 Xray 后点击应用时，会停止 Sing-box，导致 Hysteria2/TUIC 不再服务。选择 Sing-box 则会尝试将配置整体切换到该引擎，也改变了原有分区。界面虽显示双引擎状态，却没有匹配的常规应用入口。

建议：默认提供自动双引擎应用；显式单引擎切换作为高级诊断操作，展示受影响入站。

验收：VLESS + Hysteria2 混合配置通过默认按钮应用后，两引擎和两类入站均通过探测；不得仅验证接口返回 200。

## P1-2：流量统计的身份维度与 Client 模型不一致

证据：

- `backend/internal/model/proxy.go:30` 仅保证 `(inbound_id, email)` 唯一，允许多个入站使用相同 email。
- `backend/internal/service/traffic/accountant.go:142` 使用 `WHERE email = ?` 更新累计流量，没有入站或 Client ID 约束。
- `backend/internal/service/traffic/proxy_users.go:320` 附近的实时聚合也使用 email 作为键。

影响：两个不同入站存在相同 email 时，采集到的一份增量可同时累加到两个 Client 行；配额判断、汇总数据和实时展示可能混算。这是代码可确认的身份映射问题，不表示已在生产复现。

建议：引擎统计标识映射到稳定 Client ID，将展示用 email 与计费身份分离。如果产品需要跨节点共享配额，应引入明确的订阅者身份及配额账本，避免依赖重复 email 隐式共享。

验收：两个入站、同 email、不同客户端中，仅一个产生流量时只更新对应账户；同时覆盖 Xray、Sing-box 及重启/重置后的统计。

## P1-3：低内存运行与目的地址采集仍需治理

证据：

- `backend/internal/service/traffic/egress.go:161` 在落库前取走并替换 pending map；`:170` 在一个事务中逐条 upsert。
- `backend/internal/service/traffic/egress.go:185` 的 upsert 不返回错误，`:215` 的错误只写日志；外层事务失败后也没有回补。
- pending 聚合按目的地址增加，没有明确的键数预算；相关来源还包括 `backend/internal/service/traffic/proxy_users.go:64`。
- `backend/internal/config/db.go:160` 将整个数据库连接池限制为一个连接；业务读写共享该池。`:165` 已配置 WAL，不能再次建议“补上 WAL”作为完整解决方案。
- `backend/main.go:436` 的 HTTP WriteTimeout 为五分钟；多处数据库查询未绑定请求 context。
- `docs/dmit_zenithpanel_reconnect_timeout_report_2026-08-25.md` 记录了 OOM、容器重启和长时间 SQL 阻塞，并将采集与单连接争用列为可能的放大路径。该报告没有 heap profile，不能据此断言唯一内存泄漏根因。

建议：

1. 为目的地址聚合和异步处理设置容量上限、降级策略和丢弃计数。
2. 使用有界分批写入，向上传递错误；定义失败批次的重试与去重语义。
3. 评估串行写入通道与 WAL 只读连接池，避免直接放开多个写入连接。
4. 查询绑定请求取消与合理超时，采集 RSS/heap/goroutine、flush 耗时、聚合键数量和有效 journal mode。现有连接池等待指标可以继续使用。
5. 提供低资源预设，允许关闭目的地址明细及 enrichment，同时保留必要的累计流量统计。

验收：在明确的内存限制和高目的地址基数下进行持续压测；普通状态接口仍满足预设延迟目标；注入数据库失败后可恢复且不静默丢数；重启后记录有效 WAL 状态。72 小时稳定性验证应在 Linux/VPS 环境执行。

架构依据：SQLite WAL 允许不同连接的读与写并行，但仍只有一个写入者，见 [SQLite 官方 WAL 文档](https://www.sqlite.org/wal.html)。单个共享连接无法利用该读写并行能力。

## P1-4：配置保存、部署完成与代理实际生效需要统一

证据：

- `backend/internal/service/deploy/orchestrator.go:356` 的部署器明确只创建/删除入站行，假定其他路径会自动重载。
- `backend/internal/api/deploy.go:29` 创建 orchestrator 时没有接入代理管理器；`frontend/src/views/SmartDeploy.vue:117` 收到 succeeded 后直接取得客户端信息。
- `backend/internal/api/router.go:1176` 的 Client 创建只写库、清订阅缓存、返回 Created。
- `backend/internal/api/proxy_routes.go:98` 在生成/启动新配置前先停止两引擎；该整个流程没有跨请求生命周期串行化或上一版本恢复。

影响：Smart Deploy 可以报告成功但新入站尚未绑定；用户配置已保存后复制订阅，也可能尚未在引擎中生效。多个 Apply、恢复协调等操作重叠时存在交叉 stop/start 的风险；新配置错误还可能在停掉旧服务后暴露。

建议：建设统一的配置协调服务，记录配置版本以及“已保存/待应用/应用中/已生效/失败”状态；尽可能先生成并预检两个候选配置，再串行执行切换、探测和失败恢复。Smart Deploy、手动 Apply、备份恢复共用该服务。不要承诺通过重启就能实现连接无损，保留会话需要单独评估引擎能力。

验收：部署成功必须包含运行态与探测结果；错误配置不能丢失上一可用版本；并发 Apply 后数据库版本、配置文件和监听状态一致；删除或回滚后的配置不继续服务。

补充：`backend/internal/api/deploy.go:34` 的 Smart Deploy 证书管理器 ACME 依赖仍为 nil。面板其他位置已有 ACME 能力，后续应复用现有证书服务，避免智能部署中的该选项成为失败路径。

## P1-5：管理员会话需要可撤销和绝对有效期

证据：

- `backend/internal/api/middleware/auth_middleware.go:100` 校验 JWT 签名、时效和 purpose 后直接授予管理员身份，没有当前会话版本核验。
- `backend/internal/api/router.go:1696` 修改密码仅保存密码哈希。
- `backend/internal/api/security_routes.go:335` 可凭有效管理员会话续签新的 24 小时会话，没有绝对时长限制。

影响：如果 JWT/Cookie 已被获取，管理员改密码后旧会话仍然有效；只要持续刷新，还可延长其存续时间。当前 HttpOnly、SameSite 和 Origin 防护降低了部分攻击面，但不提供会话撤销。

建议：使用会话记录或管理员 session_version；密码修改、账号停用和主动“退出全部设备”时撤销旧会话，明确 2FA 变更策略；刷新保留原始登录时间并限制最大会话年龄。

验收：修改密码后旧 Bearer 与 Cookie 对受保护 API、refresh 均返回 401；新的合法登录正常工作；过了绝对时限不能通过刷新延长。

## P1-6：OTA 成功和回滚需要真实健康判定

证据：

- `backend/internal/service/updater/updater.go:250` 仅通过 `ctl status` 输出包含 `db: ok` 判定候选成功。
- `backend/internal/cli/run.go:64` 的 status 读取 health；`backend/main.go:114` 和 `:121` 的代理自动启动失败仅写警告，并不阻止面板继续启动。
- `backend/internal/service/updater/updater.go:227` 的 rollback 删除、重命名和启动步骤均吞掉错误，然后尝试写入 rolled_back 状态。
- `backend/internal/service/updater/updater_test.go:69` 检查回滚脚本关键字符串及顺序，没有实际执行容器切换。

影响：数据库健康但应运行的代理未启动时，OTA 仍可能提交并删除旧容器；回滚动作失败时可能没有准确的最终状态。

建议：按启用入站确定预期引擎，检查候选实际服务能力后再提交；逐步核验回滚，明确 rollback_failed；状态持久化不应完全依赖待恢复容器已可执行命令。加入数据库迁移兼容性与数据恢复策略，不能假定旧镜像一定能使用新版本修改过的数据。

验收：Linux 容器测试覆盖成功、候选启动失败、代理启动失败、健康超时、恢复旧容器失败；检查端口、旧容器保留策略和最终状态，而不是只检查脚本文本。

## P1-7：修正干净 checkout 与首次安装路径

### 安全扫描构建前置资源

`.github/workflows/main.yml:121` 的 security-verify 独立 job 未准备 `backend/internal/api/dist`，该目录被 `.gitignore:88` 排除，但 `backend/internal/api/embed.go:9` 必须嵌入它。其他 lint/test job 已分别准备该目录，不会共享给这个独立 job。

本轮复制相同 embed 源文件到临时最小模块，使用 Go 1.26.5 执行 `go list -json .` 得到 `pattern all:dist: no matching files found`。这验证了缺资源时的包加载失败；未远程运行 GitHub Actions。

建议统一构建前置步骤，在干净 checkout 下验证安全扫描，避免本机已有 dist 掩盖问题。规则见 [Go embed 官方说明](https://pkg.go.dev/embed)。

### Docker 快速启动端口

`README.md` 和 `README_CN.md` 给出 `-p 8080:8080`，没有设置 `ZENITH_PORT=8080`；`backend/internal/config/db.go:233` 明确首次启动随机选择 10000–65535 的面板端口。新数据目录下，README 的访问地址与实际监听不一致。

建议修正两种语言的命令并从空数据卷验证，确保文档、安装脚本和 Agent 操作说明采用一致的端口约定。代理端口映射也需要与 host/bridge 模式明确对应。

验收：全新 checkout 的所有必过 job 成功；照 README 原样启动后可以到达日志中指定的 setup 页面。

## P2：产品与工程迭代

| 方向 | 当前证据 / 缺口 | 建议交付与验收 |
|---|---|---|
| 统一订阅管理 | `backend/internal/service/sub/generator.go:351` 已按 UUID 聚合多入站；`:380` 又用首行决定配额/过期等信息。前端主要按单入站创建 Client。 | 明确订阅者、入站绑定、协议凭据、共享配额的关系；一个入口选择多个节点，提供统一订阅及重置。不要直接把“复用 UUID”当成完整账户模型，尤其要处理 SS-2022 等凭据约束。 |
| 诊断工作台 | 入站分阶段 probe 已存在；`proxy_routes.go:338` 的 test-connection 只检查 VPS 出口，完整网络诊断 API 未整合进主要 UI。 | 清晰区分出口可用、端口绑定、TLS、客户端链路；保存最近诊断时间和失败阶段。服务器出口正常不能显示为所有节点可用。 |
| OTA 状态恢复 | `SecurityView.vue:531` 把 running 进度放进 updateError；`:874` 页面挂载不加载持久化更新状态。 | 页面刷新后恢复运行中任务，进度与错误分开，展示成功版本或回滚原因。 |
| 移动端与可访问性 | Proxy 顶部操作区/多列表格存在溢出风险；公共弹窗缺少完整 dialog、焦点管理与 Escape 行为。基于源码检查，未做浏览器实测。 | 在 375px 和桌面分别测试关键操作；表格末列可达；键盘可完成确认、关闭、焦点回归；弹窗有名称和语义。 |
| 组件与 API 契约 | 当前 `ProxyView.vue` 2543 行、`SecurityView.vue` 1666 行、`router.go` 1789 行；已有拆分但大量领域逻辑仍聚合。 | 围绕入站编辑、订阅者、路由/出站、安全设置分拆；共享 DTO/校验逐步替代 any 与 JSON 字符串边界，优先覆盖真实跨层操作测试。 |
| 文档与完成标准 | README 仍把部分已实现功能列为 Planned；ROADMAP 保留多个已完成 backlog；优化设计中的完成表述超过当前端到端验证证据。 | 区分“实现完成、单测通过、容器验收、生产验证”；用当前能力清单维护路线图。 |

后续加固候选：备份恢复与正常 CRUD 共用领域校验；文件操作使用根目录约束的原子打开方式防御本地符号链接竞态。后者需要攻击者同时具备沙箱目录写入条件，应通过专门 Linux 测试确认，不能泛化成任意远程用户都能越权。

Cron 和 Docker 的 infra:write 本身是强权限。本次不把其可运行命令定性为新发现的未授权 RCE；若以后面向多人协作，可进一步拆细权限并清楚说明能力边界。

## 推荐实施顺序

1. **正确性与可交付性**：修复双引擎默认 Apply、计费身份、会话撤销、CI 前置资源和安装文档；每项补对应的故障回归用例。
2. **运行可靠性**：统一配置协调，补齐 Smart Deploy/OTA 运行态验收，治理采集容量、数据库等待与失败重试；增加 Linux 容器端到端测试和持续负载验证。
3. **日常体验**：统一订阅管理、诊断工作台、更新状态恢复、移动端与可访问性；按领域继续拆分组件和 API。

建议每个迭代以“用户操作后实际服务可用”作为完成条件，增加协议数量和多人管理应在上述基础稳定后再按实际需求排期。

## 本轮验证

- 后端 `go test ./...`：通过。首次因本机 Go 缓存目录权限失败，调整执行权限后完整通过。
- 前端 `npm test`：16/16 通过。
- 前端 `npm run build`：通过，包含 TypeScript 检查。
- 最小 embed 缺失场景：复现 `pattern all:dist: no matching files found`。
- 未执行 Docker 镜像构建、实际 OTA、真实代理端到端、浏览器移动端验证或 VPS 压测；未在本轮重新运行供应链漏洞扫描。
- 历史事故的生产事实引用自仓库内 2026-08-25 报告，本轮未重新连接该 VPS。

现有测试通过是当前基线，不构成上述未覆盖场景已经通过的证明。
