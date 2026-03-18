# ZenithPanel 核心开发指南 (Development Guide)

简体中文 | [English](development_guide.md)

欢迎参与 ZenithPanel 的开发！本文档将指导你如何在本地准备开发环境、了解代码结构、遵循团队的规范以及进行构建和测试。

---

## 1. 🛠️ 本地开发环境准备

ZenithPanel 是一个前后端分离项目。在开发阶段，我们分别启动后端的 API 服务与前端的 Dev Server 进行联调测试。

### 环境依赖
1. **Go (>= 1.24)**：用于后端开发。
2. **Node.js (>= 20)** & **npm (>= 10)**：用于前端 Vue 3 的开发。
3. **SQLite3**：后端自动处理，无需另行安装服务。
4. (可选) Docker 环境：用于测试“应用市场”及容器管理。

### 初始启动步骤
```bash
# 克隆仓库
git clone https://github.com/YourOrg/ZenithPanel.git
cd ZenithPanel

# 1. 启动后端
cd backend
go mod tidy
go run main.go

# 2. 启动前端
cd frontend
npm install
npm run dev
```

> **注意：** 在开发模式下，前端 Dev Server 会通过 Vite Proxy 将 `/api` 请求转发到后端的 8080 端口。

---

## 2. 📂 目录结构

```text
ZenithPanel/
├── backend/                  # Go 后端
│   ├── internal/             # 核心逻辑
│   │   ├── api/              # Controller 层 & 静态资源嵌入
│   │   ├── model/            # 数据库 Entity 层
│   │   └── service/          # 业务逻辑 (代理、文件、监控)
│   └── main.go               # 入口
├── frontend/                 # Vue 3 前端
│   ├── src/
│   │   ├── api/              # Axios 封装
│   │   ├── components/       # 公共组件
│   │   └── views/            # 业务页面
│   └── vite.config.ts
├── docs/                     # 架构文档与手册
├── scripts/                  # 编译与安装脚本
├── .gitignore
└── README.md
```

---

## 3. 🛡️ 代码规范

- **后端**: 遵循 Go 标准风格。使用 `internal/` 包隔离核心逻辑。
- **前端**: 使用 TypeScript 强类型约束。样式采用 Tailwind CSS。

---

## 4. 🌐 API 接口列表

### 代理管理
| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/v1/inbounds` | 获取所有入站节点 |
| POST | `/api/v1/inbounds` | 创建入站节点 |
| PUT | `/api/v1/inbounds/:id` | 更新入站节点 |
| DELETE | `/api/v1/inbounds/:id` | 删除入站节点 |
| GET | `/api/v1/clients` | 获取所有客户端 |
| POST | `/api/v1/clients` | 创建客户端（UUID 自动生成） |
| GET | `/api/v1/routing-rules` | 获取路由规则 |
| POST | `/api/v1/routing-rules` | 创建路由规则 |
| GET | `/api/v1/proxy/status` | 获取代理运行状态与启用对象数量 |
| POST | `/api/v1/proxy/apply` | 生成并应用代理配置，重启所选内核 |
| POST | `/api/v1/proxy/generate-reality-keys` | 生成 VLESS Reality 所需的 X25519 密钥对 + Short ID |
| GET | `/api/v1/proxy/config/xray` | 预览生成的 Xray 配置 JSON |
| GET | `/api/v1/proxy/config/singbox` | 预览生成的 Sing-box 配置 JSON |
| GET | `/api/v1/sub/:uuid` | 订阅接口（自动识别 Clash/Base64 格式） |

---

## 5. 📦 编译与发布

本项目支持**单文件二进制**分发：
1. `npm run build` 生成前端静态资源。
2. 资源被同步到 `backend/internal/api/dist/`。
3. `go build` 利用 `go:embed` 将静态资源打包进最终二进制。
