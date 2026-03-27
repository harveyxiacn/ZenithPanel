package sub

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/config"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/model"
	proxyservice "github.com/harveyxiacn/ZenithPanel/backend/internal/service/proxy"
)

// streamInfo holds parsed transport/TLS settings extracted from an Inbound's Stream JSON.
type streamInfo struct {
	Network  string // tcp, ws, grpc, h2, httpupgrade
	Security string // tls, reality, none
	SNI      string
	ALPN     string // comma-separated
	Flow     string // xtls-rprx-vision
	// WebSocket
	WSPath string
	WSHost string
	// gRPC
	GRPCServiceName string
	// HTTP/2
	H2Path string
	H2Host string
	// HTTPUpgrade
	HTTPUpgradePath string
	HTTPUpgradeHost string
	// Reality
	RealityPBK  string // public key
	RealitySID  string // short id
	RealitySPX  string // spiderX
	Fingerprint string
}

// parseStream extracts transport info from the Stream JSON column.
func parseStream(streamJSON string) streamInfo {
	si := streamInfo{Network: "tcp", Security: "none"}
	if streamJSON == "" || streamJSON == "{}" {
		return si
	}
	var raw map[string]interface{}
	if err := json.Unmarshal([]byte(streamJSON), &raw); err != nil {
		return si
	}

	if v, ok := raw["network"].(string); ok && v != "" {
		si.Network = v
	}
	// Normalize "wss" → "ws" + TLS (wss is WebSocket over TLS, not a real transport)
	if si.Network == "wss" {
		si.Network = "ws"
		if si.Security == "none" {
			si.Security = "tls"
		}
	}
	if v, ok := raw["security"].(string); ok && v != "" {
		si.Security = v
	}

	// TLS settings
	if tls, ok := raw["tlsSettings"].(map[string]interface{}); ok {
		if v, ok := tls["serverName"].(string); ok {
			si.SNI = v
		}
		if alpn, ok := tls["alpn"].([]interface{}); ok {
			parts := make([]string, 0, len(alpn))
			for _, a := range alpn {
				if s, ok := a.(string); ok {
					parts = append(parts, s)
				}
			}
			si.ALPN = strings.Join(parts, ",")
		}
		if v, ok := tls["fingerprint"].(string); ok {
			si.Fingerprint = v
		}
	}

	// Reality settings
	if _, ok := raw["realitySettings"].(map[string]interface{}); ok {
		info := proxyservice.ReadRealityStreamInfo(raw)
		if info.PublicKey != "" {
			si.RealityPBK = info.PublicKey
		}
		if len(info.ShortIDs) > 0 {
			si.RealitySID = info.ShortIDs[0]
		}
		if len(info.ServerNames) > 0 && si.SNI == "" {
			si.SNI = info.ServerNames[0]
		}
		if info.ServerName != "" && si.SNI == "" {
			si.SNI = info.ServerName
		}
		if info.Fingerprint != "" {
			si.Fingerprint = info.Fingerprint
		}
		if info.SpiderX != "" {
			si.RealitySPX = info.SpiderX
		}
	}

	// WebSocket settings
	if ws, ok := raw["wsSettings"].(map[string]interface{}); ok {
		if v, ok := ws["path"].(string); ok {
			si.WSPath = v
		}
		if headers, ok := ws["headers"].(map[string]interface{}); ok {
			if v, ok := headers["Host"].(string); ok {
				si.WSHost = v
			}
		}
	}

	// gRPC settings
	if grpc, ok := raw["grpcSettings"].(map[string]interface{}); ok {
		if v, ok := grpc["serviceName"].(string); ok {
			si.GRPCServiceName = v
		}
	}

	// HTTP/2 settings
	if h2, ok := raw["httpSettings"].(map[string]interface{}); ok {
		if v, ok := h2["path"].(string); ok {
			si.H2Path = v
		}
		if hosts, ok := h2["host"].([]interface{}); ok && len(hosts) > 0 {
			if v, ok := hosts[0].(string); ok {
				si.H2Host = v
			}
		}
	}

	// HTTPUpgrade settings
	if hu, ok := raw["httpupgradeSettings"].(map[string]interface{}); ok {
		if v, ok := hu["path"].(string); ok {
			si.HTTPUpgradePath = v
		}
		if v, ok := hu["host"].(string); ok {
			si.HTTPUpgradeHost = v
		}
	}

	return si
}

// parseSettingsFlow extracts the "flow" field from Settings JSON (used for VLESS).
func parseSettingsFlow(settingsJSON string) string {
	if settingsJSON == "" || settingsJSON == "{}" {
		return ""
	}
	var raw map[string]interface{}
	if err := json.Unmarshal([]byte(settingsJSON), &raw); err != nil {
		return ""
	}
	if v, ok := raw["flow"].(string); ok {
		return v
	}
	return ""
}

// getServerAddr extracts the server IP/hostname from the HTTP request.
// Falls back to the Host header (minus port).
func getServerAddr(c *gin.Context) string {
	host := c.Request.Host
	// Strip port if present
	if h, _, err := net.SplitHostPort(host); err == nil {
		return h
	}
	return host
}

// GenerateSubscription creates subscription output in Clash YAML or base64-encoded links.
func GenerateSubscription(c *gin.Context) {
	uuid := c.Param("uuid")
	format := c.Query("format") // empty, "clash", or "base64"

	var client model.Client
	if err := config.DB.Where("uuid = ?", uuid).First(&client).Error; err != nil {
		c.Status(404)
		return
	}
	if !client.Enable {
		c.Status(404)
		return
	}
	if client.ExpiryTime > 0 && time.Now().Unix() > client.ExpiryTime {
		c.Status(404)
		return
	}

	// Fetch inbounds that this client is associated with (via inbound_id).
	// A client may have multiple records with the same UUID across different inbounds.
	var clientRecords []model.Client
	config.DB.Where("uuid = ? AND enable = ?", client.UUID, true).Find(&clientRecords)

	var inboundIDs []uint
	for _, cr := range clientRecords {
		inboundIDs = append(inboundIDs, cr.InboundID)
	}

	var inbounds []model.Inbound
	if len(inboundIDs) > 0 {
		config.DB.Where("id IN ? AND enable = ?", inboundIDs, true).Find(&inbounds)
	}

	serverAddr := getServerAddr(c)

	// Determine format
	userAgent := c.GetHeader("User-Agent")
	if format == "" {
		if detectClashClient(userAgent) {
			format = "clash"
		} else {
			format = "base64"
		}
	}

	if format == "clash" {
		yamlStr := buildClashConfig(inbounds, client, serverAddr)
		c.Header("Content-Type", "text/yaml; charset=utf-8")
		c.Header("Content-Disposition", "inline; filename=\"clash.yaml\"")
		// Add subscription-userinfo header for traffic info (always send, total=0 means unlimited)
		c.Header("subscription-userinfo",
			fmt.Sprintf("upload=%d; download=%d; total=%d", client.UpLoad, client.DownLoad, client.Total))
		c.String(200, yamlStr)
		return
	}

	// Default: Standard Base64 encoded links
	links := buildBase64Links(inbounds, client, serverAddr)
	c.Header("Content-Type", "text/plain; charset=utf-8")
	c.Header("subscription-userinfo",
		fmt.Sprintf("upload=%d; download=%d; total=%d", client.UpLoad, client.DownLoad, client.Total))
	c.String(200, links)
}

func detectClashClient(ua string) bool {
	lowerUA := strings.ToLower(ua)
	clashKeywords := []string{"clash", "mihomo", "stash", "surge", "quantumult", "shadowrocket", "loon"}
	for _, kw := range clashKeywords {
		if strings.Contains(lowerUA, kw) {
			return true
		}
	}
	return false
}

// buildClashConfig generates a Clash Meta (Mihomo) YAML configuration.
func buildClashConfig(inbounds []model.Inbound, client model.Client, serverAddr string) string {
	var sb strings.Builder

	sb.WriteString("port: 7890\n")
	sb.WriteString("socks-port: 7891\n")
	sb.WriteString("allow-lan: true\n")
	sb.WriteString("mode: rule\n")
	sb.WriteString("log-level: info\n")
	sb.WriteString("unified-delay: true\n")
	sb.WriteString("\n")
	sb.WriteString("dns:\n")
	sb.WriteString("  enable: true\n")
	sb.WriteString("  enhanced-mode: fake-ip\n")
	sb.WriteString("  fake-ip-range: 198.18.0.1/16\n")
	sb.WriteString("  fake-ip-filter:\n")
	sb.WriteString("    - '*.lan'\n")
	sb.WriteString("    - '*.local'\n")
	sb.WriteString("    - 'localhost.ptlogin2.qq.com'\n")
	sb.WriteString("  nameserver:\n")
	sb.WriteString("    - https://dns.alidns.com/dns-query\n")
	sb.WriteString("    - https://doh.pub/dns-query\n")
	sb.WriteString("  fallback:\n")
	sb.WriteString("    - https://dns.google/dns-query\n")
	sb.WriteString("    - https://cloudflare-dns.com/dns-query\n")
	sb.WriteString("  fallback-filter:\n")
	sb.WriteString("    geoip: true\n")
	sb.WriteString("    geoip-code: CN\n")
	sb.WriteString("\n")
	sb.WriteString("proxies:\n")

	var proxyNames []string

	for _, in := range inbounds {
		si := parseStream(in.Stream)
		name := in.Tag
		if name == "" {
			name = fmt.Sprintf("%s-%d", in.Protocol, in.Port)
		}
		proxyNames = append(proxyNames, name)

		switch in.Protocol {
		case "vless":
			flow := parseSettingsFlow(in.Settings)
			fp := si.Fingerprint
			if fp == "" {
				fp = "chrome" // default fingerprint for client
			}
			sb.WriteString(fmt.Sprintf("  - name: \"%s\"\n", name))
			sb.WriteString("    type: vless\n")
			sb.WriteString(fmt.Sprintf("    server: %s\n", serverAddr))
			sb.WriteString(fmt.Sprintf("    port: %d\n", in.Port))
			sb.WriteString(fmt.Sprintf("    uuid: %s\n", client.UUID))
			sb.WriteString("    udp: true\n")
			if flow != "" {
				sb.WriteString(fmt.Sprintf("    flow: %s\n", flow))
			}
			// Map "httpupgrade" to "ws" for Clash compatibility
			clashNetwork := si.Network
			if clashNetwork == "httpupgrade" {
				clashNetwork = "ws"
			}
			sb.WriteString(fmt.Sprintf("    network: %s\n", clashNetwork))
			sb.WriteString(fmt.Sprintf("    client-fingerprint: %s\n", fp))
			if si.Security == "tls" {
				sb.WriteString("    tls: true\n")
				if si.SNI != "" {
					sb.WriteString(fmt.Sprintf("    servername: %s\n", si.SNI))
				}
				if si.ALPN != "" {
					sb.WriteString("    alpn:\n")
					for _, a := range strings.Split(si.ALPN, ",") {
						sb.WriteString(fmt.Sprintf("      - %s\n", strings.TrimSpace(a)))
					}
				}
				sb.WriteString("    skip-cert-verify: true\n")
			} else if si.Security == "reality" {
				sb.WriteString("    tls: true\n")
				if si.SNI != "" {
					sb.WriteString(fmt.Sprintf("    servername: %s\n", si.SNI))
				}
				sb.WriteString("    reality-opts:\n")
				if si.RealityPBK != "" {
					sb.WriteString(fmt.Sprintf("      public-key: %s\n", si.RealityPBK))
				}
				if si.RealitySID != "" {
					sb.WriteString(fmt.Sprintf("      short-id: %s\n", si.RealitySID))
				}
			}
			writeClashTransport(&sb, si)

		case "vmess":
			sb.WriteString(fmt.Sprintf("  - name: \"%s\"\n", name))
			sb.WriteString("    type: vmess\n")
			sb.WriteString(fmt.Sprintf("    server: %s\n", serverAddr))
			sb.WriteString(fmt.Sprintf("    port: %d\n", in.Port))
			sb.WriteString(fmt.Sprintf("    uuid: %s\n", client.UUID))
			sb.WriteString("    alterId: 0\n")
			sb.WriteString("    cipher: auto\n")
			sb.WriteString("    udp: true\n")
			clashNet := si.Network
			if clashNet == "httpupgrade" {
				clashNet = "ws"
			}
			sb.WriteString(fmt.Sprintf("    network: %s\n", clashNet))
			if si.Security == "tls" {
				sb.WriteString("    tls: true\n")
				if si.SNI != "" {
					sb.WriteString(fmt.Sprintf("    servername: %s\n", si.SNI))
				}
				if si.Fingerprint != "" {
					sb.WriteString(fmt.Sprintf("    client-fingerprint: %s\n", si.Fingerprint))
				}
				sb.WriteString("    skip-cert-verify: true\n")
			}
			writeClashTransport(&sb, si)

		case "trojan":
			sb.WriteString(fmt.Sprintf("  - name: \"%s\"\n", name))
			sb.WriteString("    type: trojan\n")
			sb.WriteString(fmt.Sprintf("    server: %s\n", serverAddr))
			sb.WriteString(fmt.Sprintf("    port: %d\n", in.Port))
			sb.WriteString(fmt.Sprintf("    password: %s\n", client.UUID))
			sb.WriteString("    udp: true\n")
			clashNet := si.Network
			if clashNet == "httpupgrade" {
				clashNet = "ws"
			}
			sb.WriteString(fmt.Sprintf("    network: %s\n", clashNet))
			// Trojan requires TLS unless explicitly set to "none"
			if si.Security != "none" {
				sb.WriteString("    tls: true\n")
				if si.SNI != "" {
					sb.WriteString(fmt.Sprintf("    sni: %s\n", si.SNI))
				}
				if si.Fingerprint != "" {
					sb.WriteString(fmt.Sprintf("    client-fingerprint: %s\n", si.Fingerprint))
				}
				if si.ALPN != "" {
					sb.WriteString("    alpn:\n")
					for _, a := range strings.Split(si.ALPN, ",") {
						sb.WriteString(fmt.Sprintf("      - %s\n", strings.TrimSpace(a)))
					}
				}
				sb.WriteString("    skip-cert-verify: true\n")
			}
			writeClashTransport(&sb, si)

		case "shadowsocks":
			method, password := parseSSSettings(in.Settings)
			sb.WriteString(fmt.Sprintf("  - name: \"%s\"\n", name))
			sb.WriteString("    type: ss\n")
			sb.WriteString(fmt.Sprintf("    server: %s\n", serverAddr))
			sb.WriteString(fmt.Sprintf("    port: %d\n", in.Port))
			sb.WriteString(fmt.Sprintf("    cipher: %s\n", method))
			sb.WriteString(fmt.Sprintf("    password: %s\n", password))

		case "hysteria2":
			sb.WriteString(fmt.Sprintf("  - name: \"%s\"\n", name))
			sb.WriteString("    type: hysteria2\n")
			sb.WriteString(fmt.Sprintf("    server: %s\n", serverAddr))
			sb.WriteString(fmt.Sprintf("    port: %d\n", in.Port))
			sb.WriteString(fmt.Sprintf("    password: %s\n", client.UUID))
			if si.SNI != "" {
				sb.WriteString(fmt.Sprintf("    sni: %s\n", si.SNI))
			}
			sb.WriteString("    skip-cert-verify: true\n")
		}

		sb.WriteString("\n")
	}

	// Proxy groups
	sb.WriteString("proxy-groups:\n")
	sb.WriteString("  - name: PROXY\n")
	sb.WriteString("    type: select\n")
	sb.WriteString("    proxies:\n")
	sb.WriteString("      - AUTO\n")
	for _, name := range proxyNames {
		sb.WriteString(fmt.Sprintf("      - \"%s\"\n", name))
	}
	sb.WriteString("      - DIRECT\n")
	sb.WriteString("\n")
	sb.WriteString("  - name: AUTO\n")
	sb.WriteString("    type: url-test\n")
	sb.WriteString("    proxies:\n")
	for _, name := range proxyNames {
		sb.WriteString(fmt.Sprintf("      - \"%s\"\n", name))
	}
	sb.WriteString("    url: http://www.gstatic.com/generate_204\n")
	sb.WriteString("    interval: 300\n")
	sb.WriteString("    tolerance: 50\n")
	sb.WriteString("\n")

	// Rules — prevent proxy loop: server address must go DIRECT
	sb.WriteString("rules:\n")
	if net.ParseIP(serverAddr) != nil {
		// Server is an IP address
		sb.WriteString(fmt.Sprintf("  - IP-CIDR,%s/32,DIRECT,no-resolve\n", serverAddr))
	} else {
		// Server is a domain
		sb.WriteString(fmt.Sprintf("  - DOMAIN,%s,DIRECT\n", serverAddr))
	}
	// LAN / private
	sb.WriteString("  - DOMAIN-SUFFIX,local,DIRECT\n")
	sb.WriteString("  - IP-CIDR,127.0.0.0/8,DIRECT,no-resolve\n")
	sb.WriteString("  - IP-CIDR,10.0.0.0/8,DIRECT,no-resolve\n")
	sb.WriteString("  - IP-CIDR,172.16.0.0/12,DIRECT,no-resolve\n")
	sb.WriteString("  - IP-CIDR,192.168.0.0/16,DIRECT,no-resolve\n")
	// China direct
	sb.WriteString("  - GEOIP,CN,DIRECT\n")
	// Everything else through proxy
	sb.WriteString("  - MATCH,PROXY\n")

	return sb.String()
}

// writeClashTransport appends transport-specific options (ws, grpc, h2, httpupgrade).
func writeClashTransport(sb *strings.Builder, si streamInfo) {
	switch si.Network {
	case "ws":
		sb.WriteString("    ws-opts:\n")
		if si.WSPath != "" {
			sb.WriteString(fmt.Sprintf("      path: %s\n", si.WSPath))
		}
		if si.WSHost != "" {
			sb.WriteString("      headers:\n")
			sb.WriteString(fmt.Sprintf("        Host: %s\n", si.WSHost))
		}
	case "grpc":
		sb.WriteString("    grpc-opts:\n")
		if si.GRPCServiceName != "" {
			sb.WriteString(fmt.Sprintf("      grpc-service-name: %s\n", si.GRPCServiceName))
		}
	case "h2":
		sb.WriteString("    h2-opts:\n")
		if si.H2Host != "" {
			sb.WriteString("      host:\n")
			sb.WriteString(fmt.Sprintf("        - %s\n", si.H2Host))
		}
		if si.H2Path != "" {
			sb.WriteString(fmt.Sprintf("      path: %s\n", si.H2Path))
		}
	case "httpupgrade":
		// Clash Meta supports httpupgrade as a first-class transport
		sb.WriteString("    ws-opts:\n") // httpupgrade uses ws-opts in Clash Meta
		if si.HTTPUpgradePath != "" {
			sb.WriteString(fmt.Sprintf("      path: %s\n", si.HTTPUpgradePath))
		}
		if si.HTTPUpgradeHost != "" {
			sb.WriteString("      headers:\n")
			sb.WriteString(fmt.Sprintf("        Host: %s\n", si.HTTPUpgradeHost))
		}
		sb.WriteString("      v2ray-http-upgrade: true\n")
	}
}

// buildBase64Links generates base64-encoded V2Ray/Xray subscription links.
func buildBase64Links(inbounds []model.Inbound, client model.Client, serverAddr string) string {
	var lines []string

	for _, in := range inbounds {
		si := parseStream(in.Stream)
		remark := in.Tag
		if remark == "" {
			remark = fmt.Sprintf("%s-%d", in.Protocol, in.Port)
		}

		var link string
		switch in.Protocol {
		case "vless":
			link = buildVLESSLink(in, client, serverAddr, si)
		case "vmess":
			link = buildVMessLink(in, client, serverAddr, si)
		case "trojan":
			link = buildTrojanLink(in, client, serverAddr, si, remark)
		case "shadowsocks":
			link = buildSSLink(in, serverAddr, remark)
		case "hysteria2":
			link = buildHysteria2Link(in, client, serverAddr, si, remark)
		default:
			continue
		}

		if link != "" {
			lines = append(lines, link)
		}
	}

	return base64.StdEncoding.EncodeToString([]byte(strings.Join(lines, "\n")))
}

// buildVLESSLink generates a vless:// share link.
func buildVLESSLink(in model.Inbound, client model.Client, server string, si streamInfo) string {
	flow := parseSettingsFlow(in.Settings)
	remark := in.Tag

	params := url.Values{}
	params.Set("encryption", "none")
	params.Set("type", si.Network)
	params.Set("security", si.Security)

	if flow != "" {
		params.Set("flow", flow)
	}
	if si.SNI != "" {
		params.Set("sni", si.SNI)
	}
	if si.ALPN != "" {
		params.Set("alpn", si.ALPN)
	}
	if si.Fingerprint != "" {
		params.Set("fp", si.Fingerprint)
	}

	// Reality
	if si.Security == "reality" {
		if si.RealityPBK != "" {
			params.Set("pbk", si.RealityPBK)
		}
		if si.RealitySID != "" {
			params.Set("sid", si.RealitySID)
		}
		if si.RealitySPX != "" {
			params.Set("spx", si.RealitySPX)
		}
	}

	// Transport
	switch si.Network {
	case "tcp":
		params.Set("headerType", "none")
	case "ws":
		if si.WSPath != "" {
			params.Set("path", si.WSPath)
		}
		if si.WSHost != "" {
			params.Set("host", si.WSHost)
		}
	case "grpc":
		if si.GRPCServiceName != "" {
			params.Set("serviceName", si.GRPCServiceName)
		}
		params.Set("mode", "gun")
	case "h2":
		if si.H2Path != "" {
			params.Set("path", si.H2Path)
		}
		if si.H2Host != "" {
			params.Set("host", si.H2Host)
		}
	case "httpupgrade":
		if si.HTTPUpgradePath != "" {
			params.Set("path", si.HTTPUpgradePath)
		}
		if si.HTTPUpgradeHost != "" {
			params.Set("host", si.HTTPUpgradeHost)
		}
	}

	return fmt.Sprintf("vless://%s@%s:%d?%s#%s",
		client.UUID, server, in.Port, params.Encode(), url.PathEscape(remark))
}

// buildVMessLink generates a vmess:// share link (V2RayN standard).
func buildVMessLink(in model.Inbound, client model.Client, server string, si streamInfo) string {
	vmessObj := map[string]interface{}{
		"v":    "2",
		"ps":   in.Tag,
		"add":  server,
		"port": fmt.Sprintf("%d", in.Port),
		"id":   client.UUID,
		"aid":  "0",
		"scy":  "auto",
		"net":  si.Network,
		"type": "none",
		"host": "",
		"path": "",
		"tls":  "",
		"sni":  "",
		"alpn": "",
	}
	if si.Security == "tls" {
		vmessObj["tls"] = "tls"
		vmessObj["sni"] = si.SNI
		vmessObj["alpn"] = si.ALPN
		if si.Fingerprint != "" {
			vmessObj["fp"] = si.Fingerprint
		}
	}
	switch si.Network {
	case "ws":
		vmessObj["path"] = si.WSPath
		vmessObj["host"] = si.WSHost
	case "grpc":
		vmessObj["path"] = si.GRPCServiceName
		vmessObj["type"] = "gun"
	case "h2":
		vmessObj["net"] = "h2"
		vmessObj["path"] = si.H2Path
		vmessObj["host"] = si.H2Host
	}

	jsonBytes, _ := json.Marshal(vmessObj)
	return "vmess://" + base64.StdEncoding.EncodeToString(jsonBytes)
}

// buildTrojanLink generates a trojan:// share link.
func buildTrojanLink(in model.Inbound, client model.Client, server string, si streamInfo, remark string) string {
	params := url.Values{}
	params.Set("type", si.Network)
	params.Set("security", si.Security)
	if si.SNI != "" {
		params.Set("sni", si.SNI)
	}
	if si.ALPN != "" {
		params.Set("alpn", si.ALPN)
	}
	if si.Fingerprint != "" {
		params.Set("fp", si.Fingerprint)
	}

	switch si.Network {
	case "tcp":
		params.Set("headerType", "none")
	case "ws":
		if si.WSPath != "" {
			params.Set("path", si.WSPath)
		}
		if si.WSHost != "" {
			params.Set("host", si.WSHost)
		}
	case "grpc":
		if si.GRPCServiceName != "" {
			params.Set("serviceName", si.GRPCServiceName)
		}
		params.Set("mode", "gun")
	case "h2":
		if si.H2Path != "" {
			params.Set("path", si.H2Path)
		}
		if si.H2Host != "" {
			params.Set("host", si.H2Host)
		}
	case "httpupgrade":
		if si.HTTPUpgradePath != "" {
			params.Set("path", si.HTTPUpgradePath)
		}
		if si.HTTPUpgradeHost != "" {
			params.Set("host", si.HTTPUpgradeHost)
		}
	}

	return fmt.Sprintf("trojan://%s@%s:%d?%s#%s",
		client.UUID, server, in.Port, params.Encode(), url.PathEscape(remark))
}

// buildSSLink generates a ss:// share link.
func buildSSLink(in model.Inbound, server string, remark string) string {
	method, password := parseSSSettings(in.Settings)
	userInfo := base64.URLEncoding.EncodeToString([]byte(method + ":" + password))
	return fmt.Sprintf("ss://%s@%s:%d#%s", userInfo, server, in.Port, url.PathEscape(remark))
}

// buildHysteria2Link generates a hysteria2:// share link.
func buildHysteria2Link(in model.Inbound, client model.Client, server string, si streamInfo, remark string) string {
	params := url.Values{}
	if si.SNI != "" {
		params.Set("sni", si.SNI)
	}
	params.Set("insecure", "1") // allow self-signed certs by default
	q := "?" + params.Encode()
	return fmt.Sprintf("hysteria2://%s@%s:%d%s#%s",
		client.UUID, server, in.Port, q, url.PathEscape(remark))
}

// parseSSSettings extracts method and password from Shadowsocks settings JSON.
func parseSSSettings(settingsJSON string) (method, password string) {
	method = "aes-256-gcm"
	password = ""
	if settingsJSON == "" || settingsJSON == "{}" {
		return
	}
	var raw map[string]interface{}
	if err := json.Unmarshal([]byte(settingsJSON), &raw); err != nil {
		return
	}
	if v, ok := raw["method"].(string); ok && v != "" {
		method = v
	}
	if v, ok := raw["password"].(string); ok {
		password = v
	}
	return
}
