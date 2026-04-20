package sub

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strings"
	"sync"
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
	// TLS verification
	AllowInsecure bool // user has explicitly allowed self-signed certs
}

// hysteria2Extras holds protocol-specific options read from an inbound's Settings JSON.
type hysteria2Extras struct {
	ObfsType     string // typically "salamander"
	ObfsPassword string
	Ports        string // port hopping spec, e.g. "20000-30000,40000"
	UpMbps       int
	DownMbps     int
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
	if si.Network == "wss" {
		si.Network = "ws"
		if si.Security == "none" {
			si.Security = "tls"
		}
	}
	if v, ok := raw["security"].(string); ok && v != "" {
		si.Security = v
	}

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
		if v, ok := tls["allowInsecure"].(bool); ok {
			si.AllowInsecure = v
		}
	}

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

	if grpc, ok := raw["grpcSettings"].(map[string]interface{}); ok {
		if v, ok := grpc["serviceName"].(string); ok {
			si.GRPCServiceName = v
		}
	}

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

// parseHysteria2Extras reads obfs and port-hopping hints from Hysteria2 settings JSON.
func parseHysteria2Extras(settingsJSON string) hysteria2Extras {
	var h hysteria2Extras
	if settingsJSON == "" || settingsJSON == "{}" {
		return h
	}
	var raw map[string]interface{}
	if err := json.Unmarshal([]byte(settingsJSON), &raw); err != nil {
		return h
	}
	if obfs, ok := raw["obfs"].(map[string]interface{}); ok {
		if t, ok := obfs["type"].(string); ok {
			h.ObfsType = t
		}
		if pw, ok := obfs["password"].(string); ok {
			h.ObfsPassword = pw
		}
	}
	if p, ok := raw["ports"].(string); ok {
		h.Ports = p
	}
	if v, ok := raw["up_mbps"].(float64); ok {
		h.UpMbps = int(v)
	}
	if v, ok := raw["down_mbps"].(float64); ok {
		h.DownMbps = int(v)
	}
	return h
}

// parseSSPlugin reads optional shadowsocks plugin settings (v2ray-plugin, obfs).
func parseSSPlugin(settingsJSON string) (pluginName, pluginOpts string) {
	if settingsJSON == "" || settingsJSON == "{}" {
		return
	}
	var raw map[string]interface{}
	if err := json.Unmarshal([]byte(settingsJSON), &raw); err != nil {
		return
	}
	if v, ok := raw["plugin"].(string); ok {
		pluginName = v
	}
	if v, ok := raw["plugin_opts"].(string); ok {
		pluginOpts = v
	}
	return
}

// getServerAddr extracts the server IP/hostname from the HTTP request.
func getServerAddr(c *gin.Context) string {
	host := c.Request.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		return h
	}
	return host
}

func normalizeServerAddress(raw string) string {
	addr := strings.TrimSpace(raw)
	if addr == "" {
		return ""
	}
	if parsed, err := url.Parse(addr); err == nil && parsed.Host != "" {
		addr = parsed.Host
	}
	if host, _, err := net.SplitHostPort(addr); err == nil {
		return host
	}
	return strings.Trim(addr, "[]")
}

func resolveInboundServerAddress(in model.Inbound, requestHost string) string {
	if explicit := normalizeServerAddress(in.ServerAddress); explicit != "" {
		return explicit
	}
	si := parseStream(in.Stream)
	if si.Security == "tls" {
		if sni := normalizeServerAddress(si.SNI); sni != "" {
			return sni
		}
	}
	switch si.Network {
	case "ws":
		if host := normalizeServerAddress(si.WSHost); host != "" {
			return host
		}
	case "h2":
		if host := normalizeServerAddress(si.H2Host); host != "" {
			return host
		}
	case "httpupgrade":
		if host := normalizeServerAddress(si.HTTPUpgradeHost); host != "" {
			return host
		}
	}
	return normalizeServerAddress(requestHost)
}

// Subscription response caching.
// Low-volume endpoint with expensive string building — cache by (uuid, format, host).
// TTL is short so traffic counters stay ~fresh and revoked clients drop out quickly.
const subCacheTTL = 8 * time.Second

type subCacheEntry struct {
	body        string
	contentType string
	userInfo    string
	storedAt    time.Time
}

var (
	subCacheMu sync.Mutex
	subCache   = make(map[string]subCacheEntry)
	subCacheGC time.Time
)

func subCacheGet(key string) (subCacheEntry, bool) {
	subCacheMu.Lock()
	defer subCacheMu.Unlock()
	e, ok := subCache[key]
	if !ok {
		return subCacheEntry{}, false
	}
	if time.Since(e.storedAt) > subCacheTTL {
		delete(subCache, key)
		return subCacheEntry{}, false
	}
	return e, true
}

func subCachePut(key string, e subCacheEntry) {
	subCacheMu.Lock()
	defer subCacheMu.Unlock()
	subCache[key] = e
	// Opportunistic GC so the map doesn't grow unbounded on rotating UUIDs.
	if time.Since(subCacheGC) > time.Minute {
		cutoff := time.Now().Add(-subCacheTTL)
		for k, v := range subCache {
			if v.storedAt.Before(cutoff) {
				delete(subCache, k)
			}
		}
		subCacheGC = time.Now()
	}
}

// GenerateSubscription creates subscription output in Clash YAML or base64-encoded links.
func GenerateSubscription(c *gin.Context) {
	uuid := c.Param("uuid")
	format := c.Query("format") // empty, "clash", or "base64"

	userAgent := c.GetHeader("User-Agent")
	if format == "" {
		if detectClashClient(userAgent) {
			format = "clash"
		} else {
			format = "base64"
		}
	}

	serverAddr := getServerAddr(c)
	cacheKey := uuid + "|" + format + "|" + serverAddr

	// Fast path — serve from cache if available.
	if entry, ok := subCacheGet(cacheKey); ok {
		c.Header("Content-Type", entry.contentType)
		c.Header("subscription-userinfo", entry.userInfo)
		if format == "clash" {
			c.Header("Content-Disposition", "inline; filename=\"clash.yaml\"")
		}
		c.String(200, entry.body)
		return
	}

	// One query fetches every enabled (client, inbound) pair for this UUID.
	// Previously took 3 round-trips (client by uuid, all records, all inbounds).
	type row struct {
		model.Client
		InboundTag           string `gorm:"column:inbound_tag"`
		InboundProtocol      string `gorm:"column:inbound_protocol"`
		InboundPort          int    `gorm:"column:inbound_port"`
		InboundSettings      string `gorm:"column:inbound_settings"`
		InboundStream        string `gorm:"column:inbound_stream"`
		InboundServerAddress string `gorm:"column:inbound_server_address"`
	}
	var rows []row
	err := config.DB.
		Table("clients").
		Select(`clients.*,
			inbounds.tag AS inbound_tag,
			inbounds.protocol AS inbound_protocol,
			inbounds.port AS inbound_port,
			inbounds.settings AS inbound_settings,
			inbounds.stream AS inbound_stream,
			inbounds.server_address AS inbound_server_address`).
		Joins("JOIN inbounds ON inbounds.id = clients.inbound_id AND inbounds.enable = ? AND inbounds.deleted_at IS NULL", true).
		Where("clients.uuid = ? AND clients.enable = ? AND clients.deleted_at IS NULL", uuid, true).
		Scan(&rows).Error
	if err != nil || len(rows) == 0 {
		c.Status(404)
		return
	}

	// Use first row as canonical client (all rows share the same UUID/traffic counters).
	client := rows[0].Client
	if client.ExpiryTime > 0 && time.Now().Unix() > client.ExpiryTime {
		c.Status(404)
		return
	}

	inbounds := make([]model.Inbound, 0, len(rows))
	for _, r := range rows {
		inbounds = append(inbounds, model.Inbound{
			ID:            r.InboundID,
			Tag:           r.InboundTag,
			Protocol:      r.InboundProtocol,
			Port:          r.InboundPort,
			Settings:      r.InboundSettings,
			Stream:        r.InboundStream,
			ServerAddress: r.InboundServerAddress,
		})
	}

	userInfo := fmt.Sprintf("upload=%d; download=%d; total=%d",
		client.UpLoad, client.DownLoad, client.Total)

	var body, contentType string
	if format == "clash" {
		body = buildClashConfig(inbounds, client, serverAddr)
		contentType = "text/yaml; charset=utf-8"
		c.Header("Content-Disposition", "inline; filename=\"clash.yaml\"")
	} else {
		body = buildBase64Links(inbounds, client, serverAddr)
		contentType = "text/plain; charset=utf-8"
	}

	subCachePut(cacheKey, subCacheEntry{
		body:        body,
		contentType: contentType,
		userInfo:    userInfo,
		storedAt:    time.Now(),
	})

	c.Header("Content-Type", contentType)
	c.Header("subscription-userinfo", userInfo)
	c.String(200, body)
}

// InvalidateSubCache drops all cached subscription responses. Callers should invoke
// this after mutating inbounds or clients so clients see the change within one request.
func InvalidateSubCache() {
	subCacheMu.Lock()
	subCache = make(map[string]subCacheEntry)
	subCacheMu.Unlock()
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
	sb := &strings.Builder{}
	sb.Grow(2048 + 512*len(inbounds))

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
	serverRules := make([]string, 0, len(inbounds))
	seenServerRules := make(map[string]struct{})

	for _, in := range inbounds {
		si := parseStream(in.Stream)
		publicServer := resolveInboundServerAddress(in, serverAddr)
		name := in.Tag
		if name == "" {
			name = fmt.Sprintf("%s-%d", in.Protocol, in.Port)
		}
		proxyNames = append(proxyNames, name)
		if publicServer != "" {
			if _, seen := seenServerRules[publicServer]; !seen {
				serverRules = append(serverRules, publicServer)
				seenServerRules[publicServer] = struct{}{}
			}
		}
		skipVerify := si.AllowInsecure

		switch in.Protocol {
		case "vless":
			flow := parseSettingsFlow(in.Settings)
			fp := si.Fingerprint
			if fp == "" {
				fp = "chrome"
			}
			fmt.Fprintf(sb, "  - name: \"%s\"\n", name)
			sb.WriteString("    type: vless\n")
			fmt.Fprintf(sb, "    server: %s\n", publicServer)
			fmt.Fprintf(sb, "    port: %d\n", in.Port)
			fmt.Fprintf(sb, "    uuid: %s\n", client.UUID)
			sb.WriteString("    udp: true\n")
			if flow != "" {
				fmt.Fprintf(sb, "    flow: %s\n", flow)
			}
			clashNetwork := si.Network
			if clashNetwork == "httpupgrade" {
				clashNetwork = "ws"
			}
			fmt.Fprintf(sb, "    network: %s\n", clashNetwork)
			fmt.Fprintf(sb, "    client-fingerprint: %s\n", fp)
			if si.Security == "tls" {
				sb.WriteString("    tls: true\n")
				if si.SNI != "" {
					fmt.Fprintf(sb, "    servername: %s\n", si.SNI)
				}
				writeClashALPN(sb, si.ALPN)
				fmt.Fprintf(sb, "    skip-cert-verify: %t\n", skipVerify)
			} else if si.Security == "reality" {
				sb.WriteString("    tls: true\n")
				if si.SNI != "" {
					fmt.Fprintf(sb, "    servername: %s\n", si.SNI)
				}
				sb.WriteString("    reality-opts:\n")
				if si.RealityPBK != "" {
					fmt.Fprintf(sb, "      public-key: %s\n", si.RealityPBK)
				}
				if si.RealitySID != "" {
					fmt.Fprintf(sb, "      short-id: %s\n", si.RealitySID)
				}
			}
			writeClashTransport(sb, si)

		case "vmess":
			fmt.Fprintf(sb, "  - name: \"%s\"\n", name)
			sb.WriteString("    type: vmess\n")
			fmt.Fprintf(sb, "    server: %s\n", publicServer)
			fmt.Fprintf(sb, "    port: %d\n", in.Port)
			fmt.Fprintf(sb, "    uuid: %s\n", client.UUID)
			sb.WriteString("    alterId: 0\n")
			sb.WriteString("    cipher: auto\n")
			sb.WriteString("    udp: true\n")
			clashNet := si.Network
			if clashNet == "httpupgrade" {
				clashNet = "ws"
			}
			fmt.Fprintf(sb, "    network: %s\n", clashNet)
			if si.Security == "tls" {
				sb.WriteString("    tls: true\n")
				if si.SNI != "" {
					fmt.Fprintf(sb, "    servername: %s\n", si.SNI)
				}
				if si.Fingerprint != "" {
					fmt.Fprintf(sb, "    client-fingerprint: %s\n", si.Fingerprint)
				}
				writeClashALPN(sb, si.ALPN)
				fmt.Fprintf(sb, "    skip-cert-verify: %t\n", skipVerify)
			}
			writeClashTransport(sb, si)

		case "trojan":
			fmt.Fprintf(sb, "  - name: \"%s\"\n", name)
			sb.WriteString("    type: trojan\n")
			fmt.Fprintf(sb, "    server: %s\n", publicServer)
			fmt.Fprintf(sb, "    port: %d\n", in.Port)
			fmt.Fprintf(sb, "    password: %s\n", client.UUID)
			sb.WriteString("    udp: true\n")
			clashNet := si.Network
			if clashNet == "httpupgrade" {
				clashNet = "ws"
			}
			fmt.Fprintf(sb, "    network: %s\n", clashNet)
			if si.Security == "reality" {
				sb.WriteString("    tls: true\n")
				if si.SNI != "" {
					fmt.Fprintf(sb, "    sni: %s\n", si.SNI)
				}
				if si.Fingerprint != "" {
					fmt.Fprintf(sb, "    client-fingerprint: %s\n", si.Fingerprint)
				}
				sb.WriteString("    reality-opts:\n")
				if si.RealityPBK != "" {
					fmt.Fprintf(sb, "      public-key: %s\n", si.RealityPBK)
				}
				if si.RealitySID != "" {
					fmt.Fprintf(sb, "      short-id: %s\n", si.RealitySID)
				}
			} else if si.Security != "none" {
				sb.WriteString("    tls: true\n")
				if si.SNI != "" {
					fmt.Fprintf(sb, "    sni: %s\n", si.SNI)
				}
				if si.Fingerprint != "" {
					fmt.Fprintf(sb, "    client-fingerprint: %s\n", si.Fingerprint)
				}
				writeClashALPN(sb, si.ALPN)
				fmt.Fprintf(sb, "    skip-cert-verify: %t\n", skipVerify)
			}
			writeClashTransport(sb, si)

		case "shadowsocks":
			method, password := parseSSSettings(in.Settings)
			fmt.Fprintf(sb, "  - name: \"%s\"\n", name)
			sb.WriteString("    type: ss\n")
			fmt.Fprintf(sb, "    server: %s\n", publicServer)
			fmt.Fprintf(sb, "    port: %d\n", in.Port)
			fmt.Fprintf(sb, "    cipher: %s\n", method)
			fmt.Fprintf(sb, "    password: %s\n", password)
			sb.WriteString("    udp: true\n")

		case "hysteria2":
			extras := parseHysteria2Extras(in.Settings)
			fmt.Fprintf(sb, "  - name: \"%s\"\n", name)
			sb.WriteString("    type: hysteria2\n")
			fmt.Fprintf(sb, "    server: %s\n", publicServer)
			fmt.Fprintf(sb, "    port: %d\n", in.Port)
			if extras.Ports != "" {
				fmt.Fprintf(sb, "    ports: %s\n", extras.Ports)
			}
			fmt.Fprintf(sb, "    password: %s\n", client.UUID)
			if si.SNI != "" {
				fmt.Fprintf(sb, "    sni: %s\n", si.SNI)
			}
			if extras.ObfsType != "" {
				fmt.Fprintf(sb, "    obfs: %s\n", extras.ObfsType)
				if extras.ObfsPassword != "" {
					fmt.Fprintf(sb, "    obfs-password: %s\n", extras.ObfsPassword)
				}
			}
			if extras.UpMbps > 0 {
				fmt.Fprintf(sb, "    up: \"%d Mbps\"\n", extras.UpMbps)
			}
			if extras.DownMbps > 0 {
				fmt.Fprintf(sb, "    down: \"%d Mbps\"\n", extras.DownMbps)
			}
			writeClashALPN(sb, si.ALPN)
			fmt.Fprintf(sb, "    skip-cert-verify: %t\n", skipVerify)
		}

		sb.WriteString("\n")
	}

	sb.WriteString("proxy-groups:\n")
	sb.WriteString("  - name: PROXY\n")
	sb.WriteString("    type: select\n")
	sb.WriteString("    proxies:\n")
	sb.WriteString("      - AUTO\n")
	for _, name := range proxyNames {
		fmt.Fprintf(sb, "      - \"%s\"\n", name)
	}
	sb.WriteString("      - DIRECT\n")
	sb.WriteString("\n")
	sb.WriteString("  - name: AUTO\n")
	sb.WriteString("    type: url-test\n")
	sb.WriteString("    proxies:\n")
	for _, name := range proxyNames {
		fmt.Fprintf(sb, "      - \"%s\"\n", name)
	}
	sb.WriteString("    url: http://www.gstatic.com/generate_204\n")
	sb.WriteString("    interval: 300\n")
	sb.WriteString("    tolerance: 50\n")
	sb.WriteString("\n")

	sb.WriteString("rules:\n")
	for _, ruleServer := range serverRules {
		if net.ParseIP(ruleServer) != nil {
			fmt.Fprintf(sb, "  - IP-CIDR,%s/32,DIRECT,no-resolve\n", ruleServer)
			continue
		}
		fmt.Fprintf(sb, "  - DOMAIN,%s,DIRECT\n", ruleServer)
	}
	sb.WriteString("  - DOMAIN-SUFFIX,local,DIRECT\n")
	sb.WriteString("  - IP-CIDR,127.0.0.0/8,DIRECT,no-resolve\n")
	sb.WriteString("  - IP-CIDR,10.0.0.0/8,DIRECT,no-resolve\n")
	sb.WriteString("  - IP-CIDR,172.16.0.0/12,DIRECT,no-resolve\n")
	sb.WriteString("  - IP-CIDR,192.168.0.0/16,DIRECT,no-resolve\n")
	sb.WriteString("  - GEOIP,CN,DIRECT\n")
	sb.WriteString("  - MATCH,PROXY\n")

	return sb.String()
}

// writeClashALPN writes an alpn block if the ALPN string is non-empty.
func writeClashALPN(sb *strings.Builder, alpn string) {
	if alpn == "" {
		return
	}
	sb.WriteString("    alpn:\n")
	for _, a := range strings.Split(alpn, ",") {
		if a = strings.TrimSpace(a); a != "" {
			fmt.Fprintf(sb, "      - %s\n", a)
		}
	}
}

// writeClashTransport appends transport-specific options (ws, grpc, h2, httpupgrade).
func writeClashTransport(sb *strings.Builder, si streamInfo) {
	switch si.Network {
	case "ws":
		sb.WriteString("    ws-opts:\n")
		if si.WSPath != "" {
			fmt.Fprintf(sb, "      path: %s\n", si.WSPath)
		}
		if si.WSHost != "" {
			sb.WriteString("      headers:\n")
			fmt.Fprintf(sb, "        Host: %s\n", si.WSHost)
		}
	case "grpc":
		sb.WriteString("    grpc-opts:\n")
		if si.GRPCServiceName != "" {
			fmt.Fprintf(sb, "      grpc-service-name: %s\n", si.GRPCServiceName)
		}
	case "h2":
		sb.WriteString("    h2-opts:\n")
		if si.H2Host != "" {
			sb.WriteString("      host:\n")
			fmt.Fprintf(sb, "        - %s\n", si.H2Host)
		}
		if si.H2Path != "" {
			fmt.Fprintf(sb, "      path: %s\n", si.H2Path)
		}
	case "httpupgrade":
		sb.WriteString("    ws-opts:\n") // Clash Meta uses ws-opts with v2ray-http-upgrade flag
		if si.HTTPUpgradePath != "" {
			fmt.Fprintf(sb, "      path: %s\n", si.HTTPUpgradePath)
		}
		if si.HTTPUpgradeHost != "" {
			sb.WriteString("      headers:\n")
			fmt.Fprintf(sb, "        Host: %s\n", si.HTTPUpgradeHost)
		}
		sb.WriteString("      v2ray-http-upgrade: true\n")
	}
}

// buildBase64Links generates base64-encoded V2Ray/Xray subscription links.
func buildBase64Links(inbounds []model.Inbound, client model.Client, serverAddr string) string {
	lines := make([]string, 0, len(inbounds))

	for _, in := range inbounds {
		si := parseStream(in.Stream)
		publicServer := resolveInboundServerAddress(in, serverAddr)
		remark := in.Tag
		if remark == "" {
			remark = fmt.Sprintf("%s-%d", in.Protocol, in.Port)
		}

		var link string
		switch in.Protocol {
		case "vless":
			link = buildVLESSLink(in, client, publicServer, si)
		case "vmess":
			link = buildVMessLink(in, client, publicServer, si)
		case "trojan":
			link = buildTrojanLink(in, client, publicServer, si, remark)
		case "shadowsocks":
			link = buildSSLink(in, publicServer, remark)
		case "hysteria2":
			link = buildHysteria2Link(in, client, publicServer, si, remark)
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
	if si.AllowInsecure {
		params.Set("allowInsecure", "1")
	}

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

	appendTransportParams(params, si)

	return fmt.Sprintf("vless://%s@%s:%d?%s#%s",
		client.UUID, server, in.Port, params.Encode(), url.PathEscape(remark))
}

// buildVMessLink generates a vmess:// share link (V2RayN JSON format).
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
		if si.AllowInsecure {
			vmessObj["allowInsecure"] = "1"
		}
	} else if si.Security == "reality" {
		vmessObj["tls"] = "reality"
		vmessObj["sni"] = si.SNI
		if si.Fingerprint != "" {
			vmessObj["fp"] = si.Fingerprint
		}
		if si.RealityPBK != "" {
			vmessObj["pbk"] = si.RealityPBK
		}
		if si.RealitySID != "" {
			vmessObj["sid"] = si.RealitySID
		}
		if si.RealitySPX != "" {
			vmessObj["spx"] = si.RealitySPX
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
	case "httpupgrade":
		// V2RayN reads httpupgrade as "httpupgrade" net kind with path/host fields.
		vmessObj["net"] = "httpupgrade"
		vmessObj["path"] = si.HTTPUpgradePath
		vmessObj["host"] = si.HTTPUpgradeHost
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
	if si.AllowInsecure {
		params.Set("allowInsecure", "1")
	}
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

	appendTransportParams(params, si)

	return fmt.Sprintf("trojan://%s@%s:%d?%s#%s",
		url.PathEscape(client.UUID), server, in.Port, params.Encode(), url.PathEscape(remark))
}

// appendTransportParams sets the transport-specific query fields for vless/trojan URIs.
func appendTransportParams(params url.Values, si streamInfo) {
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
}

// buildSSLink generates a ss:// share link per SIP002. Supports plugin hints.
func buildSSLink(in model.Inbound, server string, remark string) string {
	method, password := parseSSSettings(in.Settings)
	if method == "" || password == "" {
		return ""
	}
	// SIP002: userinfo is base64url(method:password) without padding.
	userInfo := base64.RawURLEncoding.EncodeToString([]byte(method + ":" + password))
	base := fmt.Sprintf("ss://%s@%s:%d", userInfo, server, in.Port)

	pluginName, pluginOpts := parseSSPlugin(in.Settings)
	if pluginName != "" {
		q := url.Values{}
		if pluginOpts != "" {
			q.Set("plugin", pluginName+";"+pluginOpts)
		} else {
			q.Set("plugin", pluginName)
		}
		base += "/?" + q.Encode()
	}
	return base + "#" + url.PathEscape(remark)
}

// buildHysteria2Link generates a hysteria2:// share link.
func buildHysteria2Link(in model.Inbound, client model.Client, server string, si streamInfo, remark string) string {
	extras := parseHysteria2Extras(in.Settings)
	params := url.Values{}
	if si.SNI != "" {
		params.Set("sni", si.SNI)
	}
	if si.AllowInsecure {
		params.Set("insecure", "1")
	}
	if si.ALPN != "" {
		params.Set("alpn", si.ALPN)
	}
	if extras.ObfsType != "" {
		params.Set("obfs", extras.ObfsType)
		if extras.ObfsPassword != "" {
			params.Set("obfs-password", extras.ObfsPassword)
		}
	}
	if extras.Ports != "" {
		params.Set("mport", extras.Ports)
	}

	q := ""
	if encoded := params.Encode(); encoded != "" {
		q = "?" + encoded
	}
	return fmt.Sprintf("hysteria2://%s@%s:%d%s#%s",
		url.PathEscape(client.UUID), server, in.Port, q, url.PathEscape(remark))
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
