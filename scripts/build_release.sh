#!/bin/bash
# ZenithPanel 本地跨平台打包脚本 (Linux/macOS WSL -> Linux AMD64)

set -e

echo -e "\033[32m=========================================================\033[0m"
echo -e "\033[32m       📦 ZenithPanel 本地 Release 打包脚本 (Bash) 📦       \033[0m"
echo -e "\033[32m=========================================================\033[0m"

ROOT_DIR="$(pwd)"
RELEASE_DIR="$ROOT_DIR/release"
TARBALL="$ROOT_DIR/zenithpanel-release.tar.gz"

# 1. 准备目录
rm -rf "$RELEASE_DIR"
mkdir -p "$RELEASE_DIR/dist"
mkdir -p "$RELEASE_DIR/data"
mkdir -p "$RELEASE_DIR/logs"

# 2. 编译前端
echo -e "\033[36m🎨 1/4 正在编译 Vue 3 前端静态资源...\033[0m"
cd "$ROOT_DIR/frontend"
npm install
npm run build
cp -r "$ROOT_DIR/frontend/dist/"* "$RELEASE_DIR/dist/"

# 3. 准备嵌入
echo -e "\033[36m📦 2/4 正在同步静态资源到后端以支持 go:embed...\033[0m"
EMBED_DIR="$ROOT_DIR/backend/internal/api/dist"
rm -rf "$EMBED_DIR"
mkdir -p "$EMBED_DIR"
cp -r "$ROOT_DIR/frontend/dist/"* "$EMBED_DIR/"

# 4. 跨平台编译 Go 后端
echo -e "\033[36m⚙️  3/4 正在跨平台编译 Go 后端 (linux/amd64)...\033[0m"
cd "$ROOT_DIR/backend"
# 禁用 CGO 以确保生成的二进制是完全静态链接的
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "-s -w" -o "$RELEASE_DIR/zenithpanel" main.go

# 4. 归档打包
echo -e "\033[36m🗄️ 3/3 正在打包为 $TARBALL ...\033[0m"
cd "$ROOT_DIR"
rm -f "$TARBALL"
tar -czvf "$TARBALL" -C "$RELEASE_DIR" .

echo -e "\033[32m=========================================================\033[0m"
echo -e "\033[32m✅ 打包完成！\033[0m"
echo -e "\033[35m请通过 FTP/SFTP/SCP 将位于项目根目录的 \033[0m"
echo -e "\033[33m   -> zenithpanel-release.tar.gz \033[0m"
echo -e "\033[35m以及 scripts/install.sh 一起上传到您的 VPS 服务器。\033[0m"
echo -e "\033[32m上传后在 VPS 执行: bash install.sh\033[0m"
echo -e "\033[32m=========================================================\033[0m"
