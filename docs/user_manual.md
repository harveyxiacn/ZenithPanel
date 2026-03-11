# ZenithPanel - 极客面板用户使用手册

## 📖 简介
ZenithPanel 是一款面向外贸/商旅极客的全方位 VPS 管理与科学上网代理核心编排面板。提供基于 Vue 3 + Tailwind CSS 的现代化沉浸式 UI 和 Go 语言驱动的极低内存开销后端。极度适配 1C1G 小型 VPS 方案。

---

## 🚀 启动与安装

> [!TIP]
> **由于本项目现已公开 (Public)**，GitHub Actions 是**完全免费且无限量使用**的。每次代码 Push 到 `main` 分支都会触发自动构建。

### 方案一：使用 GitHub 自动构建镜像 (推荐)
如果您想直接使用 GitHub 自动构建好的镜像：
1. 代码 Push 后查看仓库的 **Actions** 标签页，等待构建完成。
2. 在您的服务器上拉取镜像并运行（见方案三）。

由于 ZenithPanel 包含极低资源开销的前后端，您可以直接在本地（Windows/Mac）编译它，生成一个脱离依赖的绿色安装包并上传到 VPS：

1. **本地执行打包**
   在项目根目录下，Windows 用户在 PowerShell 执行 `./scripts/build_release.ps1` （Mac/Linux 执行 `bash scripts/build_release.sh`）。
   这会在根目录生成一个 `zenithpanel-release.tar.gz` 文件。

2. **上传到服务器**
   使用 SCP / SFTP / 宝塔 等工具将以下两个文件上传到 VPS 的**同一个目录** (如 `/root`):
   - `zenithpanel-release.tar.gz`
   - `scripts/install.sh`

3. **进入 VPS 执行安装**
   ```bash
   bash install.sh
   ```
这一步脚本会自动解压包，安装所需的 Docker 环境，并将服务端配置为 `systemd` 守护进程开机自启。

### 方案二：Docker / Docker Compose 启动
```bash
docker run -d \
  -p 8080:8080 \
  -v /opt/zenithpanel/data:/opt/zenithpanel/data \
  --name zenithpanel \
  --restart always \
  ghcr.io/harveyxiacn/zenithpanel:latest
```
运行后执行 `docker logs zenithpanel` 查看初始化向导进入地址和临时安全密码。

---

## 🛡️ 首次安全初始化向导 (Setup Wizard)
> **非常重要**：为了防止面板直接暴露公网导致配置和服务器失陷，首次运行必须在终端查看日志以获取临时密码和安全短链！

1. 浏览器打开日志提示的 URL，如：`http://ip:8080/zenith-setup-AbcD123`
2. 使用日志内生成的 16 位**一次性密码**登录向导。
3. 在系统向导中设置正式的管理员 **账户名** 与 **密码**，并自定义后续的面版入口路径。
4. Setup 成功完成后，以上初始 URL 彻底失效！

---

## ⚙️ 核心功能模块

### Dashboard (系统展示大盘)
- 实时预览您的底层主机状态 (CPU / 内存 / 磁盘使用率) 以及历史连通性、核心进程运行状态。

### 服务器管理 (Servers)
完全替代了例如 1Panel 等控制面板的臃肿基础功能，保留最轻巧的系统入口：
- **Web Terminal**: 全屏沉浸、低延迟的基于 WebSocket 的系统后台仿真 SSH。
- **File Manager**: 安全运行在 `/home` 沙箱，防止越权，随时查看和批量下载编辑配置文件。
- **Docker 守护进程**: 一页管理您的所有运行容器、控制开关。

### 代理节点分发中心 (Proxy Services)
这套系统的核心特色——全方位融合 V2ray/Xray 和 Sing-box 两大核心网络通信基石引擎。
1. **Nodes (入站节点)**：支持开启各协议入站并配置端口，并实时挂载证书进行 TLS 连接鉴权。
2. **Users**: 针对节点配置客户并附带到期时间控制和历史上下行流量记录。
3. **Sub (聚合订阅)**：一键复制动态生成的订阅 URL！无论是移动端采用 Clash(yaml)、Surge 或安卓采用的 V2ray(Base64)，客户端 UA 全端适配下发最新配置图谱。

---

## 💡 进阶：如何开启新的节点服务？

1. 进入 `Proxy` 面板，选择 `Nodes -> Add Inbound`
2. 选择要部署的协议 (如 `VLESS + TCP + XTLS` 或 `Hysteria2`)
3. 输入欲监听的端口号，为节点设置备注，提交！
4. 随后请前往 `Users` 界面为此节点发配一个用户。
5. 进入 `Subscriptions` 面板并点击链接复制按钮，在客户端更新即可直接连接体验！
