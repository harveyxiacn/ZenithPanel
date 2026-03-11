<#
.SYNOPSIS
    ZenithPanel 本地跨平台打包脚本 (Windows -> Linux AMD64)
.DESCRIPTION
    此脚本用于在本地 Windows 环境下编译 Vue 前端和 Go 后端 (目标架构 Linux AMD64)，
    并打包成了 zenithpanel-release.tar.gz 供手动上传到 VPS。
#>

Write-Host "=========================================================" -ForegroundColor Green
Write-Host "       📦 ZenithPanel 本地 Release 打包脚本 (Windows) 📦       " -ForegroundColor Green
Write-Host "=========================================================" -ForegroundColor Green

$RootDir = "e:\Project\ZenithPanel"
$ReleaseDir = "$RootDir\release"
$BackendDir = "$RootDir\backend"
$FrontendDir = "$RootDir\frontend"
$TarBall = "$RootDir\zenithpanel-release.tar.gz"

# 1. 准备目录
if (Test-Path -Path $ReleaseDir) { Remove-Item -Recurse -Force $ReleaseDir }
New-Item -ItemType Directory -Path $ReleaseDir | Out-Null
New-Item -ItemType Directory -Path "$ReleaseDir/dist" | Out-Null
New-Item -ItemType Directory -Path "$ReleaseDir/data" | Out-Null
New-Item -ItemType Directory -Path "$ReleaseDir/logs" | Out-Null

# 2. 编译前端
Write-Host "🎨 1/4 正在编译 Vue 3 前端静态资源..." -ForegroundColor Cyan
Set-Location $FrontendDir
npm install
npm run build
if ($LASTEXITCODE -ne 0) { Write-Host "❌ 前端编译失败" -ForegroundColor Red; exit 1 }
Copy-Item -Recurse -Force "$FrontendDir\dist\*" "$ReleaseDir\dist\"

# 3. 准备嵌入
Write-Host "📦 2/4 正在同步静态资源到后端以支持 go:embed..." -ForegroundColor Cyan
$EmbedDir = "$BackendDir\internal\api\dist"
if (Test-Path $EmbedDir) { Remove-Item -Recurse -Force $EmbedDir }
New-Item -ItemType Directory -Path $EmbedDir | Out-Null
Copy-Item -Recurse -Force "$FrontendDir\dist\*" "$EmbedDir\"

# 4. 跨平台编译 Go 后端
Write-Host "⚙️  3/4 正在跨平台编译 Go 后端 (linux/amd64)..." -ForegroundColor Cyan
Set-Location $BackendDir
$env:GOOS = "linux"
$env:GOARCH = "amd64"
$env:CGO_ENABLED = "0"
go build -ldflags "-s -w" -o "$ReleaseDir/zenithpanel" main.go
if ($LASTEXITCODE -ne 0) { Write-Host "❌ 后端编译失败" -ForegroundColor Red; exit 1 }

# 恢复环境变量
$env:GOOS = ""
$env:GOARCH = ""

# 4. 归档打包
Write-Host "🗄️ 3/3 正在打包为 $TarBall ..." -ForegroundColor Cyan
Set-Location $RootDir
# 使用内置 tar 工具打包
if (Test-Path $TarBall) { Remove-Item -Force $TarBall }
tar -czvf $TarBall -C $ReleaseDir .

Write-Host "=========================================================" -ForegroundColor Green
Write-Host "✅ 打包完成！" -ForegroundColor Green
Write-Host "请通过 FTP/SFTP/SCP 将位于项目根目录的" -ForegroundColor Magenta
Write-Host "   -> zenithpanel-release.tar.gz " -ForegroundColor Yellow
Write-Host "以及 scripts/install.sh 一起上传到您的 VPS 服务器。" -ForegroundColor Magenta
Write-Host "上传后在 VPS 执行: bash install.sh --local" -ForegroundColor Green
Write-Host "=========================================================" -ForegroundColor Green
