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
)

// streamInfo holds parsed transport/TLS settings extracted from an Inbound's Stream JSON.
type streamInfo struct {
	Network  string // tcp, ws, grpc, h2, kcp, quic
	Security string // tls, reality, none
	SNI      string
	ALPN     string // comma-separated
	Flow     string // xtls-rprx-vision
	// WebSocket
	WSPath string
	WSHost string
	// gRPC
	GRPCServiceName string
	// Reality
	RealityPBK   string // public key
	RealitySID   string // short id
	Fingerprint  string
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
	if reality, ok := raw["realitySettings"].(map[string]interface{}); ok {
		if v, ok := reality["publicKey"].(string); ok {
			si.RealityPBK = v
		}
		if sids, ok := reality["shortIds"].([]interface{}); ok && len(sids) > 0 {
			if v, ok := sids[0].(string); ok {
				si.RealitySID = v
			}
		}
		if names, ok := reality["serverNames"].([]interface{}); ok && len(names) > 0 {
			if v, ok := names[0].(string); ok && si.SNI == "" {
				si.SNI = v
			}
		}
		if v, ok := reality["fingerprint"].(string); ok {
			si.Fingerprint = v
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
		c.String(404, "User not found")
		return
	}
	if !client.Enable {
		c.String(403, "Account disabled")
		return
	}
	if client.ExpiryTime > 0 && time.Now().Unix() > client.ExpiryTime {
		c.String(403, "Account expired")
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
		// Add subscription-userinfo header for traffic info
		if client.Total > 0 {
			c.Header("subscription-userinfo",
				fmt.Sprintf("upload=%d; download=%d; total=%d", client.UpLoad, client.DownLoad, client.Total))
		}
		c.String(200, yamlStr)
		return
	}

	// Default: Standard Base64 encoded links
	links := buildBase64Links(inbounds, client, serverAddr)
	c.Header("Content-Type", "text/plain; charset=utf-8")
	if client.Total > 0 {
		c.Header("subscription-userinfo",
			fmt.Sprintf("upload=%d; download=%d; total=%d", client.UpLoad, client.DownLoad, client.Total))
	}
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
			sb.WriteString(fmt.Sprintf("  - name: \"%s\"\n", name))
			sb.WriteString("    type: vless\n")
			sb.WriteString(fmt.Sprintf("    server: %s\n", serverAddr))
			sb.WriteString(fmt.Sprintf("    port: %d\n", in.Port))
			sb.WriteString(fmt.Sprintf("    uuid: %s\n", client.UUID))
			sb.WriteString("    udp: true\n")
			if flow != "" {
				sb.WriteString(fmt.Sprintf("    flow: %s\n", flow))
			}
			sb.WriteString(fmt.Sprintf("    network: %s\n", si.Network))
			if si.Security == "tls" {
				sb.WriteString("    tls: true\n")
				if si.SNI != "" {
					sb.WriteString(fmt.Sprintf("    servername: %s\n", si.SNI))
				}
				if si.Fingerprint != "" {
					sb.WriteString(fmt.Sprintf("    client-fingerprint: %s\n", si.Fingerprint))
				}
				if si.ALPN != "" {
					sb.WriteString(fmt.Sprintf("    alpn:\n"))
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
				if si.Fingerprint != "" {
					sb.WriteString(fmt.Sprintf("    client-fingerprint: %s\n", si.Fingerprint))
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
			sb.WriteString(fmt.Sprintf("    network: %s\n", si.Network))
			if si.Security == "tls" {
				sb.WriteString("    tls: true\n")
				if si.SNI != "" {
					sb.WriteString(fmt.Sprintf("    servername: %s\n", si.SNI))
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
			sb.WriteString(fmt.Sprintf("    network: %s\n", si.Network))
			if si.Security == "tls" || si.Security == "" {
				sb.WriteString("    tls: true\n")
				if si.SNI != "" {
					sb.WriteString(fmt.Sprintf("    sni: %s\n", si.SNI))
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
		}

		sb.WriteString("\n")
	}

	// Proxy groups
	sb.WriteString("proxy-groups:\n")
	sb.WriteString("  - name: PROXY\n")
	sb.WriteString("    type: select\n")
	sb.WriteString("    proxies:\n")
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
	sb.WriteString("\n")

	// Rules
	sb.WriteString("rules:\n")
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

// writeClashTransport appends WebSocket or gRPC transport options.
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
	}

	// Transport
	if si.Network == "ws" {
		if si.WSPath != "" {
			params.Set("path", si.WSPath)
		}
		if si.WSHost != "" {
			params.Set("host", si.WSHost)
		}
	} else if si.Network == "grpc" {
		if si.GRPCServiceName != "" {
			params.Set("serviceName", si.GRPCServiceName)
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
	}
	if si.Network == "ws" {
		vmessObj["path"] = si.WSPath
		vmessObj["host"] = si.WSHost
	} else if si.Network == "grpc" {
		vmessObj["path"] = si.GRPCServiceName
		vmessObj["type"] = "gun"
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
	if si.Network == "ws" {
		if si.WSPath != "" {
			params.Set("path", si.WSPath)
		}
		if si.WSHost != "" {
			params.Set("host", si.WSHost)
		}
	} else if si.Network == "grpc" {
		if si.GRPCServiceName != "" {
			params.Set("serviceName", si.GRPCServiceName)
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
	q := ""
	if len(params) > 0 {
		q = "?" + params.Encode()
	}
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
