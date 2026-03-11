# ZenithPanel 核心开发指南 (Development Guide)

欢迎参与 ZenithPanel 的开发！本文档将指导你如何在本地准备开发环境、了解代码结构、遵循团队的规范以及进行构建和测试。

---

## 1. 🛠️ 本地开发环境准备

ZenithPanel 是一个前后端分离项目。在开发阶段，我们分别启动后端的 API 服务与前端的 Dev Server 进行联调测试。

### 环境依赖
1. **Go (>= 1.24)**：用于后端开发。
2. **Node.js (>= 20)** & **pnpm (>= 8)**：用于前端 Vue 3 / React 的开发。
3. **SQLite3**：多数系统内置或借助 CGO/纯 Go SQLite 驱动自动处理，无需另行安装服务。
4. (可选) Docker 环境：如果你要进行“应用市场”及容器管理的测试。

### 初始启动步骤
```bash
# 克隆仓库
git clone https://github.com/YourOrg/ZenithPanel.git
cd ZenithPanel

# 1. 启动后端 (假设默认端口 8080)
cd backend
go mod tidy
go run main.go --dev

# 2. 开新终端启动前端 (配置代理指向后端 8080)
cd frontend
pnpm install
pnpm run dev
```

> **注意：** 在 `--dev` 模式下，后端不会再去读取内嵌(embed)的前端资源，并且会对 API 接口放宽某些跨域限制以便联调。

---

## 2. 📂 推荐代码目录结构

```text
ZenithPanel/
├── backend/                  # Go 后端代码
│   ├── cmd/                  # 包含入口 main.go
│   ├── internal/             # 核心逻辑 (私有，无法被其他项目 import)
│   │   ├── api/              # HTTP 接口 Controller 层
│   │   ├── config/           # 面板自身设置读取与结构体
│   │   ├── core/             # 代理核心(Xray/Sing-box)调配调度控制器
│   │   ├── docker/           # 容器管理的 SDK 封装
│   │   ├── ssh/              # WebSocket 终端实现
│   │   ├── model/            # 数据库 Entity 层 (GORM/Ent)
│   │   └── service/          # 业务逻辑层 (路由渲染、证书申请等)
│   ├── pkg/                  # 公共可复用模块 (Utils 工具函数等)
│   ├── build/                # Dockerfile 和 编译脚本
│   └── go.mod
├── frontend/                 # 前端代码
│   ├── src/
│   │   ├── api/              # Axios 请求封装与拦截器
│   │   ├── assets/           # 静态图片、字体资源
│   │   ├── components/       # 公共业务或 UI 组件
│   │   ├── layout/           # 面板整体骨架 (侧边栏、顶栏)
│   │   ├── pages/            # 核心业务页面 (仪表盘、路由规则、容器列表)
│   │   ├── router/           # 前端路由与权限守卫拦截
│   │   ├── store/            # 状态管理 (Pinia / Zustand)
│   │   └── utils/            # 前端通用工具类
│   ├── .env.development      # 开发期间使用的环境变量 (如 API_BASE_URL)
│   ├── package.json
│   └── vite.config.ts        # Vite 配置 (用于 proxy 代理后端请求)
├── docs/                     # 架构文档和用户手册存放地
├── .gitignore
├── README.md
└── task.md                   # 开发规划看板
```

---

## 3. 🛡️ 代码规范与最佳实践

### 后端 (Go) 规范
- **命名规范**: 遵循标准 Go 风格。接口(interface)尽量用 `er` 结尾（如 `ConfigRenderer`），首字母大小写严格控制包内可见性和导出可见性。
- **错误处理**: 不要默默忽略错误 (`_`)。对前端返回错误信息时，使用标准化的 Response 结构：
  ```go
  type Response struct {
      Code int         `json:"code"`
      Msg  string      `json:"msg"`
      Data interface{} `json:"data"`
  }
  ```
- **依赖注入与单例**: 尽量在 `main.go` 初始化好如 DB连接、Logger 和 Config，再通过传参传递给具体的 Service，而非在 Service 层写死全局变量。

### 前端规范
- **TypeScript 强类型**: 从 API 获取的任何数据，必须有对应的 `interface` 或 `type` 约束。
- **CSS 方案**: 大量使用 TailwindCSS 的原子类 (`text-lg text-gray-500 font-bold`)。避免在组件文件中混杂大量的普通 `<style>` 样式，保持组件树干净。
- **组件拆分**: 当页面的 DOM 层级过深或者某个弹窗逻辑较复时，应抽离为单一责任的小组件放入 `/components` 中。

---

## 4. 📦 编译与发布架构

当代码推送到 `main` 分支并且触发 Tag (`v1.x.x`)，将会触发 GitHub Actions。

**单一二进制文件构建流程：**
1. 编译前端工程：进入 `frontend/` 执行 `pnpm run build`，生成的生产环境静态文件输出到 `frontend/dist/`。
2. 前端资源植入：在后端的某个文件中，有一段配置通过 `//go:embed ../frontend/dist/*` 指令包裹住前端。
3. 交叉编译跨平台二进制：执行 `go build`（根据目标平台调整 `GOOS` 和 `GOARCH`，如 `linux/amd64`，`linux/arm64`）。
4. 对编译出来的单个文件，附加到 GitHub Release 即可完成发布。

使用者部署时，只需要下载这一个二进制文件执行 `./zenith-panel --port=8080` 即可运行全部功能！
