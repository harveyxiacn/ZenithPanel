#!/bin/bash
###############################################################
#  ZenithPanel - VPS 全方位网络体检报告诊断脚本 (浏览器拟真版)
#
#  更新说明：
#  1. 增加了 User-Agent 伪装，模拟最新版 Chrome 浏览器
#  2. 智能解析 Cloudflare 盾：如果遇到 403，会自动读取网页源码。
###############################################################

R='\033[0;31m';G='\033[0;32m';Y='\033[1;33m';B='\033[1;34m';C='\033[0;36m';M='\033[0;35m';W='\033[1;37m';N='\033[0m'
T=15
UA="Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"

P="${G}✅ 畅通${N}"
F="${R}❌ 拦截${N}"
WN="${Y}⚠️ 受限${N}"

ph(){ echo "";echo -e "${C}╔════════════════════════════════════════════════════════════╗${N}";echo -e "${C}║${N}  ${W}$1${N}";echo -e "${C}╚════════════════════════════════════════════════════════════╝${N}"; }
ps(){ echo "";echo -e "  ${B}━━━ $1 ━━━${N}"; }
pr(){ printf "  %-24s %b" "$1" "$2";[ -n "$3" ]&&echo -e "  ${C}($3)${N}"||echo ""; }

gi(){
  local c="curl -s --max-time $T"
  local d=$($c "https://ipinfo.io/json" 2>/dev/null)
  local ip=$(echo "$d"|grep -oP '"ip"\s*:\s*"\K[^"]+' 2>/dev/null|head -1)
  local ct=$(echo "$d"|grep -oP '"city"\s*:\s*"\K[^"]+' 2>/dev/null|head -1)
  local cn=$(echo "$d"|grep -oP '"country"\s*:\s*"\K[^"]+' 2>/dev/null|head -1)
  local og=$(echo "$d"|grep -oP '"org"\s*:\s*"\K[^"]+' 2>/dev/null|head -1)
  echo -e "  ${W}IP:${N} ${G}${ip:-?}${N}  ${W}位置:${N} ${ct:-?}, ${cn:-?}  ${W}ASN:${N} ${og:-?}"
  echo -e "  ${W}类型:${N} $(echo "$og"|grep -qiE 'cloudflare|warp'&&echo -e "${M}WARP代理${N}"||echo "$og"|grep -qiE 'residential|cable|telecom|isp|bell|rogers|telus|shaw|videotron|cogeco|sasktel'&&echo -e "${G}住宅ISP ★★★★★${N}"||echo -e "${Y}商业/DC IP${N}")"
}

tc(){
  local c="curl -sL --max-time $T -A '$UA' -w '\nHTTP_STATUS:%{http_code}'"
  local resp=$(eval $c "\"$2\"" 2>/dev/null)
  local code=$(echo "$resp" | grep -oP "HTTP_STATUS:\K\d+" | tail -1)
  local body=$(echo "$resp" | sed '$d')

  case $code in
    200|301|302|307|308) 
      pr "$1" "$P" "状态码:$code"
      ;;
    403) 
      if echo "$body" | grep -qiE "Just a moment|Cloudflare|Please wait while we verify|Challenge"; then
        pr "$1" "${G}✅ 畅通${N}" "浏览器可用 (CF盾验证码)"
      elif echo "$body" | grep -qiE "Access denied|Attention Required|error 1020|blocked"; then
        pr "$1" "$F" "IP被彻底封锁 (Access denied)"
      else
        pr "$1" "$F" "状态码 403"
      fi
      ;;
    429) 
      pr "$1" "$WN" "429限流"
      ;;
    000|"") 
      pr "$1" "$F" "超时"
      ;;
    *) 
      pr "$1" "$WN" "状态码:$code"
      ;;
  esac
}

tl(){
  echo "";echo -e "  ${M}⏱️ 延迟测试 (直连服务器):${N}"
  for u in google.com claude.ai chatgpt.com youtube.com netflix.com;do
    local c="curl -s -o /dev/null --max-time $T -A '$UA' -w '%{time_total}'"
    local t=$(eval $c "https://$u" 2>/dev/null);local ms=$(echo "$t"|awk '{printf "%.0f",$1*1000}' 2>/dev/null)
    [ "$ms" -gt 0 ] 2>/dev/null&&printf "  %-20s ${G}${ms}ms${N}\n" "$u"||printf "  %-20s ${R}超时${N}\n" "$u"
  done
}

clear;echo ""
echo -e "${W}╔══════════════════════════════════════════════════════════════════════╗${N}"
echo -e "${W}║  ${C}🌏 VPS 浏览器拟真级网络体检 (ZenithPanel 集成版)${N}                ${W}║${N}"
echo -e "${W}╚══════════════════════════════════════════════════════════════════════╝${N}"
echo -e "  ${Y}时间: $(date '+%Y-%m-%d %H:%M:%S')${N}"

ph "📡 原生 IP 直连 (带 Chrome 伪装指纹)"
ps "🌐 IP 出口";gi ""

ps "🤖 AI 平台"
tc "Claude.ai" "https://claude.ai"
tc "ChatGPT" "https://chatgpt.com"
tc "Google Gemini" "https://gemini.google.com"
tc "MS Copilot" "https://copilot.microsoft.com"
tc "Perplexity" "https://www.perplexity.ai"
tc "Meta AI" "https://www.meta.ai"
tc "Poe" "https://poe.com"
tc "DeepSeek" "https://chat.deepseek.com"
tc "Midjourney" "https://www.midjourney.com"

ps "🎬 流媒体平台"
tc "Netflix" "https://www.netflix.com/title/80018499"
tc "YouTube" "https://www.youtube.com/premium"
tc "Disney+" "https://www.disneyplus.com"
tc "TikTok" "https://www.tiktok.com"
tc "Spotify" "https://open.spotify.com"
tc "HBO Max" "https://play.max.com"
tc "Prime Video" "https://www.primevideo.com"

tl ""

echo ""
echo -e "${C}══════════════════════════════════════════════════════════════════════${N}"
echo -e "  ${Y}💡 ZenithPanel 诊断报告完成${N}"
echo -e "${C}══════════════════════════════════════════════════════════════════════${N}"
