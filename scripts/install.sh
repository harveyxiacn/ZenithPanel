#!/bin/bash
# ZenithPanel 一键安装脚本 (Docker 部署, 支持 amd64 / arm64)
# 用法: curl -fsSL https://raw.githubusercontent.com/harveyxiacn/ZenithPanel/main/scripts/install.sh | sudo bash
#
# 可选环境变量:
#   ZENITH_VERSION=v1.2.3   镜像标签 (默认 latest)
#   ZENITH_PORT=31310       面板端口 (默认首次启动随机生成, 之后保存在数据库中)
#   ZENITH_DATA=/opt/zenithpanel/data   数据目录 (升级/重装都会保留)
#   ZENITH_IMAGE=ghcr.io/harveyxiacn/zenithpanel   镜像仓库 (可改为镜像加速地址或本地构建的镜像)
#
# 面板以 --network host 运行, Xray / Sing-box 直接监听宿主机端口;
# 需要 --privileged 以便面板管理 iptables / sysctl / BBR。

set -euo pipefail

REPO="harveyxiacn/ZenithPanel"
IMAGE="${ZENITH_IMAGE:-ghcr.io/harveyxiacn/zenithpanel}"
CONTAINER="zenithpanel"
TAG="${ZENITH_VERSION:-latest}"
DATA_DIR="${ZENITH_DATA:-/opt/zenithpanel/data}"

log()  { echo "[ZenithPanel] $*"; }
ok()   { echo "[ZenithPanel] ✓ $*"; }
warn() { echo "[ZenithPanel] ! $*" >&2; }
err()  { echo "[ZenithPanel] ✗ $*" >&2; exit 1; }

echo "========================================================="
echo "         ZenithPanel 一键安装脚本"
echo "========================================================="

[ "$(id -u)" -eq 0 ] || err "请使用 root 用户运行此脚本 (sudo bash install.sh)"

case "$(uname -m)" in
  x86_64 | amd64)   ARCH=amd64 ;;
  aarch64 | arm64)  ARCH=arm64 ;;
  *) err "不支持的系统架构: $(uname -m) (仅支持 amd64 / arm64)" ;;
esac
log "系统架构: $ARCH"

command -v curl &>/dev/null || err "缺少依赖命令: curl"

# ─── Docker ──────────────────────────────────────────────────────────────────
if ! command -v docker &>/dev/null; then
  log "正在安装 Docker..."
  if ! curl -fsSL https://get.docker.com | sh; then
    warn "get.docker.com 安装失败, 尝试使用发行版软件源..."
    if command -v apt-get &>/dev/null; then
      apt-get update -y -qq && apt-get install -y -qq docker.io
    elif command -v dnf &>/dev/null; then
      dnf install -y -q docker
    elif command -v yum &>/dev/null; then
      yum install -y -q docker
    else
      err "无法自动安装 Docker, 请手动安装后重试"
    fi
  fi
  ok "Docker 安装完成"
else
  ok "Docker 已安装, 跳过"
fi
systemctl enable --now docker &>/dev/null || true
docker info &>/dev/null || err "Docker 守护进程未运行"

# 旧版本 (二进制 + systemd) 安装会占用同一端口, 先停用
if systemctl list-unit-files zenithpanel.service &>/dev/null \
   && systemctl is-enabled --quiet zenithpanel.service 2>/dev/null; then
  warn "检测到旧的 systemd 服务 zenithpanel.service, 正在停用 (数据保留)"
  systemctl disable --now zenithpanel.service || true
fi

# ─── 部署容器 ────────────────────────────────────────────────────────────────
log "正在拉取镜像 ${IMAGE}:${TAG} ..."
if ! docker pull "${IMAGE}:${TAG}"; then
  docker image inspect "${IMAGE}:${TAG}" &>/dev/null || err "镜像拉取失败: ${IMAGE}:${TAG}"
  warn "拉取失败, 使用本地已有镜像 ${IMAGE}:${TAG}"
fi

mkdir -p "$DATA_DIR"

if docker container inspect "$CONTAINER" &>/dev/null; then
  log "正在替换已有容器 $CONTAINER (数据目录 $DATA_DIR 保留)..."
  docker rm -f "$CONTAINER" >/dev/null
fi

RUN_ARGS=(
  -d --name "$CONTAINER"
  --restart always
  --network host
  --pid host
  --privileged
  -v "$DATA_DIR:/opt/zenithpanel/data"
  -v /var/run/docker.sock:/var/run/docker.sock
)
[ -n "${ZENITH_PORT:-}" ] && RUN_ARGS+=(-e "ZENITH_PORT=${ZENITH_PORT}")

docker run "${RUN_ARGS[@]}" "${IMAGE}:${TAG}" >/dev/null
ok "容器已启动"

# 便于在宿主机上使用 zenithctl
cat > /usr/local/bin/zenithctl <<EOF
#!/bin/sh
exec docker exec -i $CONTAINER /opt/zenithpanel/zenithpanel ctl "\$@"
EOF
chmod +x /usr/local/bin/zenithctl

# ─── 等待就绪并输出访问信息 ──────────────────────────────────────────────────
PORT=""
for _ in $(seq 1 30); do
  PORT=$(docker logs "$CONTAINER" 2>&1 | grep -oE 'listening on [^ ]*:[0-9]+' | tail -1 | grep -oE '[0-9]+$' || true)
  [ -z "$PORT" ] && PORT=$(docker logs "$CONTAINER" 2>&1 | grep -oE 'http://<YOUR_IP>:[0-9]+' | tail -1 | grep -oE '[0-9]+$' || true)
  [ -n "$PORT" ] && break
  sleep 1
done

echo "========================================================="
echo " ✓ ZenithPanel ($TAG, $ARCH) 安装完成！"
echo ""
if docker logs "$CONTAINER" 2>&1 | grep -q "zenith-setup-"; then
  echo " 首次安装 — 请打开以下设置向导 URL 并使用一次性密码完成初始化:"
  docker logs "$CONTAINER" 2>&1 | grep -E "URL:|Password:" | tail -2 | sed 's/^/ /'
else
  echo " 面板端口: ${PORT:-<见 docker logs $CONTAINER>}"
fi
echo ""
echo " 注意: 若服务器启用了防火墙 (ufw / iptables / 云厂商安全组),"
echo "       需放行面板端口; 更安全的做法是仅通过 SSH 隧道访问:"
echo "       ssh -L ${PORT:-<port>}:127.0.0.1:${PORT:-<port>} user@<服务器IP>"
echo ""
echo " 管理命令:"
echo "   docker logs -f $CONTAINER      # 实时日志"
echo "   docker restart $CONTAINER      # 重启"
echo "   zenithctl status               # 命令行管理"
echo " 项目主页: https://github.com/${REPO}"
echo "========================================================="
