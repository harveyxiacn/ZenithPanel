#!/bin/bash
# ZenithPanel 自动化一键安装脚本

set -e

echo "========================================================="
echo "               🚀 欢迎使用 ZenithPanel 一键安装脚本 🚀"
echo "========================================================="

# 检查 root 权限
if [ "$EUID" -ne 0 ]; then
  echo "❌ 错误: 请使用 root 用户运行此脚本。"
  exit 1
fi

# 安装基础依赖
echo "📦 正在安装必要的系统依赖..."
apt-get update -y
apt-get install -y curl wget git jq nginx sysstat lsof tar

# 安装 Docker (应用容器管理所需)
if ! command -v docker &> /dev/null; then
    echo "🐳 正在安装 Docker..."
    curl -fsSL https://get.docker.com | bash
    systemctl enable docker
    systemctl start docker
else
    echo "🐳 Docker 已安装，跳过。"
fi

INSTALL_DIR="/opt/zenithpanel"
echo "⚙️  正在部署 ZenithPanel 到 $INSTALL_DIR ..."
mkdir -p "$INSTALL_DIR"

if [ -f "zenithpanel-release.tar.gz" ]; then
    echo "🗂️  检测到本地的 zenithpanel-release.tar.gz 包，正在解压..."
    tar -xzvf zenithpanel-release.tar.gz -C "$INSTALL_DIR"
    chmod +x "$INSTALL_DIR/zenithpanel"
else
    echo "❌ 错误: 未在其运行目录下找到 zenithpanel-release.tar.gz。"
    echo "请在您的本地 Windows 运行 scripts/build_release.ps1 打包，然后将 .tar.gz 上传到服务器该脚本的同级目录。"
    exit 1
fi

# 生成 Systemd 服务
echo "🔧 配置 Systemd 服务..."
cat << EOF > /etc/systemd/system/zenithpanel.service
[Unit]
Description=ZenithPanel Management System
After=network.target docker.service

[Service]
Type=simple
WorkingDirectory=$INSTALL_DIR
ExecStart=$INSTALL_DIR/zenithpanel
Restart=on-failure
RestartSec=5s

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable zenithpanel
systemctl start zenithpanel
systemctl status zenithpanel --no-pager || true

echo "========================================================="
echo "✅ ZenithPanel 安装完成！"
echo "请使用浏览器访问 http://<你的IP>:8080 以继续 Web 端安全初始化向导。"
echo "请使用以下命令查看系统服务日志以获取初次生成的随机安全密码："
echo -e "\033[1;32m   journalctl -u zenithpanel -n 50 --no-pager\033[0m"
echo "========================================================="

