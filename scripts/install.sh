#!/bin/bash
# ZenithPanel 一键安装脚本
# 用法: curl -fsSL https://raw.githubusercontent.com/harveyxiacn/ZenithPanel/main/scripts/install.sh | bash
# 可选环境变量: ZENITH_VERSION=v1.2.3 (默认使用最新 release)

set -euo pipefail

REPO="harveyxiacn/ZenithPanel"
INSTALL_DIR="/opt/zenithpanel"
SERVICE_NAME="zenithpanel"
BINARY_NAME="zenithpanel"

# ─── 辅助函数 ────────────────────────────────────────────────────────────────

log()  { echo "[ZenithPanel] $*"; }
ok()   { echo "[ZenithPanel] ✓ $*"; }
err()  { echo "[ZenithPanel] ✗ $*" >&2; exit 1; }

# 检测系统架构 → amd64 / arm64
detect_arch() {
  case "$(uname -m)" in
    x86_64)           echo "amd64" ;;
    aarch64 | arm64)  echo "arm64" ;;
    *)  err "不支持的系统架构: $(uname -m)" ;;
  esac
}

# 获取 GitHub 最新 release 版本号
fetch_latest_version() {
  local version
  version=$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" \
    | grep '"tag_name"' | head -1 | sed 's/.*"tag_name": *"\([^"]*\)".*/\1/')
  [ -n "$version" ] || err "无法从 GitHub API 获取最新版本号，请检查网络或手动指定 ZENITH_VERSION 环境变量"
  echo "$version"
}

# ─── 主流程 ──────────────────────────────────────────────────────────────────

echo "========================================================="
echo "         ZenithPanel 一键安装脚本"
echo "========================================================="

# 检查 root 权限
[ "$EUID" -eq 0 ] || err "请使用 root 用户运行此脚本"

# 检查必要工具
for cmd in curl tar; do
  command -v "$cmd" &>/dev/null || err "缺少依赖命令: $cmd"
done

ARCH=$(detect_arch)
log "系统架构: $ARCH"

# 确定目标版本
VERSION="${ZENITH_VERSION:-}"
if [ -z "$VERSION" ]; then
  log "正在获取最新版本号..."
  VERSION=$(fetch_latest_version)
fi
log "目标版本: $VERSION"

# 构造下载 URL（优先使用本地包）
TARBALL="zenithpanel-${VERSION}-linux-${ARCH}.tar.gz"
TARBALL_SHA="${TARBALL}.sha256"
DOWNLOAD_BASE="https://github.com/${REPO}/releases/download/${VERSION}"

if [ -f "$TARBALL" ]; then
  ok "检测到本地包 $TARBALL，跳过下载"
else
  log "正在下载 $TARBALL ..."
  curl -fsSL --progress-bar -o "$TARBALL" "${DOWNLOAD_BASE}/${TARBALL}" \
    || err "下载失败: ${DOWNLOAD_BASE}/${TARBALL}"

  # 校验 SHA256（如果 release 包含 .sha256 文件）
  if curl -fsSL -o "$TARBALL_SHA" "${DOWNLOAD_BASE}/${TARBALL_SHA}" 2>/dev/null; then
    log "正在校验 SHA256..."
    sha256sum -c "$TARBALL_SHA" || err "SHA256 校验失败，请重试或检查下载完整性"
    ok "SHA256 校验通过"
    rm -f "$TARBALL_SHA"
  else
    log "未找到 SHA256 文件，跳过校验"
  fi
fi

# 安装基础依赖（apt / yum 自动判断）
log "正在安装系统依赖..."
if command -v apt-get &>/dev/null; then
  apt-get update -y -qq
  apt-get install -y -qq curl wget jq sysstat lsof tar
elif command -v yum &>/dev/null; then
  yum install -y -q curl wget jq sysstat lsof tar
elif command -v dnf &>/dev/null; then
  dnf install -y -q curl wget jq sysstat lsof tar
else
  log "警告: 未识别的包管理器，跳过依赖安装"
fi

# 安装 Docker
if ! command -v docker &>/dev/null; then
  log "正在安装 Docker..."
  curl -fsSL https://get.docker.com | bash
  systemctl enable docker
  systemctl start docker
  ok "Docker 安装完成"
else
  ok "Docker 已安装，跳过"
fi

# 部署二进制
log "正在部署 ZenithPanel 到 $INSTALL_DIR ..."
mkdir -p "$INSTALL_DIR"

# 停止旧服务（如果已运行）
if systemctl is-active --quiet "$SERVICE_NAME" 2>/dev/null; then
  log "正在停止旧版本服务..."
  systemctl stop "$SERVICE_NAME"
fi

tar -xzf "$TARBALL" -C "$INSTALL_DIR" --strip-components=0
chmod +x "$INSTALL_DIR/$BINARY_NAME"
ok "二进制文件部署完成"

# 生成 Systemd 服务
log "配置 Systemd 服务..."
cat > /etc/systemd/system/${SERVICE_NAME}.service <<EOF
[Unit]
Description=ZenithPanel Management System
Documentation=https://github.com/${REPO}
After=network.target docker.service

[Service]
Type=simple
WorkingDirectory=${INSTALL_DIR}
ExecStart=${INSTALL_DIR}/${BINARY_NAME}
Restart=on-failure
RestartSec=5s
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable "$SERVICE_NAME"
systemctl start "$SERVICE_NAME"

# 等待服务就绪
sleep 2
if systemctl is-active --quiet "$SERVICE_NAME"; then
  ok "服务启动成功"
else
  log "服务状态:"
  systemctl status "$SERVICE_NAME" --no-pager || true
fi

echo "========================================================="
echo " ✓ ZenithPanel $VERSION 安装完成！"
echo ""
echo " 访问面板: http://<你的服务器IP>:8080"
echo " 查看初始安全密码:"
echo "   journalctl -u zenithpanel -n 50 --no-pager"
echo ""
echo " 管理命令:"
echo "   systemctl status $SERVICE_NAME    # 查看状态"
echo "   systemctl restart $SERVICE_NAME   # 重启服务"
echo "   journalctl -u $SERVICE_NAME -f    # 实时日志"
echo "========================================================="
