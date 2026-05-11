package proxy

import (
	"encoding/json"
	"fmt"
	"net"
	"os/exec"
	"strconv"
	"strings"

	"github.com/harveyxiacn/ZenithPanel/backend/internal/config"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/model"
)

type SingboxManager struct {
	BaseCore
}

func NewSingboxManager() *SingboxManager {
	return &SingboxManager{
		BaseCore: BaseCore{
			BinaryPath: "sing-box",
			ConfigPath: "data/singbox_config.json",
		},
	}
}

func (s *SingboxManager) GenerateConfig() (string, error) {
	var inbounds []model.Inbound
	var rules []model.RoutingRule
	var customOutbounds []model.Outbound

	config.DB.Where("enable = ?", true).Find(&inbounds)
	config.DB.Where("enable = ?", true).Find(&rules)
	config.DB.Where("enable = ?", true).Find(&customOutbounds)
	rules = UniqueRoutingRules(rules)

	clientsByInbound := fetchClientsByInbound(inbounds)

	// Build route rules: sniff first, then hijack-dns, then user rules
	routeRules := []interface{}{
		map[string]interface{}{
			"action":  "sniff",
			"timeout": "300ms",
		},
		map[string]interface{}{
			"protocol": "dns",
			"action":   "hijack-dns",
		},
	}
	for _, r := range rules {
		if !r.Enable {
			continue
		}
		ruleMap := buildSingboxRoutingRule(r)
		if ruleMap != nil {
			routeRules = append(routeRules, ruleMap)
		}
	}

	// Per-client bandwidth caps: emit a route rule for each client with SpeedLimit > 0.
	// Sing-box v1.11 supports override_download_bandwidth / override_upload_bandwidth on
	// route rules matching inbound + inbound_user.
	inboundTagByID := make(map[uint]string, len(inbounds))
	for _, in := range inbounds {
		inboundTagByID[in.ID] = in.Tag
	}
	for _, clientList := range clientsByInbound {
		for _, c := range clientList {
			if c.SpeedLimit <= 0 || !c.Enable {
				continue
			}
			tag, ok := inboundTagByID[c.InboundID]
			if !ok {
				continue
			}
			// Convert bytes/sec → Mbps (1 MB/s = 8 Mbps), minimum 1 mbps
			mbps := (c.SpeedLimit * 8) / (1024 * 1024)
			if mbps < 1 {
				mbps = 1
			}
			routeRules = append(routeRules, map[string]interface{}{
				"type": "logical",
				"mode": "and",
				"rules": []interface{}{
					map[string]interface{}{
						"inbound":      []string{tag},
						"inbound_user": []string{c.Email},
					},
				},
				"action":                      "route",
				"outbound":                    "direct",
				"override_download_bandwidth": fmt.Sprintf("%d mbps", mbps),
				"override_upload_bandwidth":   fmt.Sprintf("%d mbps", mbps),
			})
		}
	}

	// System outbounds always present
	outbounds := []interface{}{
		map[string]interface{}{"type": "direct", "tag": "direct"},
		map[string]interface{}{"type": "block", "tag": "block"},
		map[string]interface{}{"type": "dns", "tag": "dns-out"},
	}
	// Append user-defined custom outbounds (e.g. WireGuard/WARP)
	for _, ob := range customOutbounds {
		entry := buildSingboxOutbound(ob)
		if entry != nil {
			outbounds = append(outbounds, entry)
		}
	}

	primary, secondary := resolveDNSServers()
	singboxConfig := map[string]interface{}{
		"log": map[string]interface{}{
			"level": "warn",
		},
		"dns": map[string]interface{}{
			"servers": []interface{}{
				map[string]interface{}{"tag": "dns-primary", "address": primary},
				map[string]interface{}{"tag": "dns-secondary", "address": secondary},
				map[string]interface{}{"tag": "dns-local", "address": "local"},
			},
			"strategy": "prefer_ipv4",
			"final":    "dns-primary",
		},
		"inbounds":  []interface{}{},
		"outbounds": outbounds,
		"route": map[string]interface{}{
			"rules":                 routeRules,
			"final":                 "direct",
			"auto_detect_interface": true,
		},
	}

	for _, in := range inbounds {
		inboundEntry, err := buildSingboxInbound(in, clientsByInbound[in.ID])
		if err != nil {
			return "", fmt.Errorf("build singbox inbound %q: %w", in.Tag, err)
		}
		singboxConfig["inbounds"] = append(singboxConfig["inbounds"].([]interface{}), inboundEntry)
	}

	// Enable Clash API for real-time connection inspection if toggled in settings.
	if config.GetSetting("singbox_clash_api_enabled") == "true" {
		port := config.GetSetting("singbox_clash_api_port")
		if port == "" {
			port = "9090"
		}
		singboxConfig["experimental"] = map[string]interface{}{
			"clash_api": map[string]interface{}{
				"external_controller": "127.0.0.1:" + port,
				"secret":              "",
			},
		}
	}

	return PrettifyJSON(singboxConfig)
}

// buildSingboxInbound constructs a sing-box inbound with users, TLS, and transport.
// Clients are injected from the supplied list so the caller can batch-fetch them.
func buildSingboxInbound(in model.Inbound, clients []model.Client) (map[string]interface{}, error) {
	listen := "::"
	if in.Listen != "" {
		listen = in.Listen
	}

	entry := map[string]interface{}{
		"type":        in.Protocol,
		"tag":         in.Tag,
		"listen":      listen,
		"listen_port": in.Port,
	}

	// Parse settings JSON for protocol-specific options
	var settingsRaw map[string]interface{}
	if in.Settings != "" && in.Settings != "{}" {
		if err := json.Unmarshal([]byte(in.Settings), &settingsRaw); err != nil {
			return nil, fmt.Errorf("parse settings: %w", err)
		}
	}

	switch in.Protocol {
	case "vless":
		users := make([]map[string]interface{}, 0, len(clients))
		for _, c := range clients {
			u := map[string]interface{}{
				"name": c.Email,
				"uuid": c.UUID,
			}
			// Add flow if configured (e.g., xtls-rprx-vision)
			if settingsRaw != nil {
				if flow, ok := settingsRaw["flow"].(string); ok && flow != "" {
					u["flow"] = flow
				}
			}
			users = append(users, u)
		}
		entry["users"] = users

	case "vmess":
		users := make([]map[string]interface{}, 0, len(clients))
		for _, c := range clients {
			users = append(users, map[string]interface{}{
				"name":    c.Email,
				"uuid":    c.UUID,
				"alterId": 0,
			})
		}
		entry["users"] = users

	case "trojan":
		users := make([]map[string]interface{}, 0, len(clients))
		for _, c := range clients {
			users = append(users, map[string]interface{}{
				"name":     c.Email,
				"password": c.UUID,
			})
		}
		entry["users"] = users

	case "shadowsocks":
		// Shadowsocks uses method + password from settings.
		// AEAD-2022 methods support per-user multi-user via a "users" array in sing-box.
		if settingsRaw != nil {
			method, _ := settingsRaw["method"].(string)
			if method != "" {
				entry["method"] = method
			}
			if password, ok := settingsRaw["password"].(string); ok {
				entry["password"] = password
			}
			if strings.HasPrefix(method, "2022-blake3") && len(clients) > 0 {
				users := make([]map[string]interface{}, 0, len(clients))
				for _, c := range clients {
					users = append(users, map[string]interface{}{
						"name":     c.Email,
						"password": c.UUID,
					})
				}
				entry["users"] = users
			}
		}

	case "hysteria2":
		users := make([]map[string]interface{}, 0, len(clients))
		for _, c := range clients {
			users = append(users, map[string]interface{}{
				"name":     c.Email,
				"password": c.UUID,
			})
		}
		entry["users"] = users
		// Forward Hysteria2 protocol options so the server actually applies what
		// the subscription link advertises. Without this, clients negotiating
		// salamander obfuscation or bandwidth hints would mismatch the server.
		if settingsRaw != nil {
			if obfs, ok := settingsRaw["obfs"].(map[string]interface{}); ok {
				cleaned := map[string]interface{}{}
				if t, ok := obfs["type"].(string); ok && t != "" {
					cleaned["type"] = t
				}
				if pw, ok := obfs["password"].(string); ok && pw != "" {
					cleaned["password"] = pw
				}
				if len(cleaned) > 0 {
					entry["obfs"] = cleaned
				}
			}
			if v, ok := settingsRaw["up_mbps"].(float64); ok && v > 0 {
				entry["up_mbps"] = int(v)
			}
			if v, ok := settingsRaw["down_mbps"].(float64); ok && v > 0 {
				entry["down_mbps"] = int(v)
			}
			if v, ok := settingsRaw["masquerade"].(string); ok && v != "" {
				entry["masquerade"] = v
			}
			if v, ok := settingsRaw["ignore_client_bandwidth"].(bool); ok {
				entry["ignore_client_bandwidth"] = v
			}
		}

	case "tuic":
		// TUIC v5: each user has a UUID + password. We reuse the client's UUID
		// for both (same pattern as Hysteria2 / VLESS) unless the settings JSON
		// supplies a dedicated per-user password. congestion_control defaults
		// to "bbr" which is the sing-box recommendation.
		users := make([]map[string]interface{}, 0, len(clients))
		for _, c := range clients {
			users = append(users, map[string]interface{}{
				"name":     c.Email,
				"uuid":     c.UUID,
				"password": c.UUID,
			})
		}
		entry["users"] = users
		if settingsRaw != nil {
			if cc, ok := settingsRaw["congestion_control"].(string); ok && cc != "" {
				entry["congestion_control"] = cc
			}
			if um, ok := settingsRaw["udp_relay_mode"].(string); ok && um != "" {
				entry["udp_relay_mode"] = um
			}
			if zrh, ok := settingsRaw["zero_rtt_handshake"].(bool); ok {
				entry["zero_rtt_handshake"] = zrh
			}
		}
		if _, ok := entry["congestion_control"]; !ok {
			entry["congestion_control"] = "bbr"
		}
	}

	// Parse and apply stream settings (TLS + transport)
	if in.Stream != "" && in.Stream != "{}" {
		var stream map[string]interface{}
		if err := json.Unmarshal([]byte(in.Stream), &stream); err != nil {
			return nil, fmt.Errorf("parse stream: %w", err)
		}
		// Trojan requires TLS in sing-box; reject early rather than letting sing-box
		// refuse to start (which would take the entire engine down).
		if in.Protocol == "trojan" {
			sec, _ := stream["security"].(string)
			if sec != "tls" && sec != "reality" {
				return nil, fmt.Errorf("trojan inbound %q requires TLS or Reality security (got %q) — "+
					"set stream security to 'tls' or 'reality'", in.Tag, sec)
			}
		}
		applyStreamToSingbox(entry, stream)

		// Optional connection multiplexing (smux/yamux/h2mux). Sing-box treats
		// multiplex as a sibling of transport, so it lives directly on the inbound.
		if mux, ok := stream["multiplex"].(map[string]interface{}); ok {
			if enabled, _ := mux["enabled"].(bool); enabled {
				proto := "smux"
				if p, ok := mux["protocol"].(string); ok && p != "" {
					proto = p
				}
				entry["multiplex"] = map[string]interface{}{
					"enabled":         true,
					"protocol":        proto,
					"max_connections": 4,
					"min_streams":     4,
				}
			}
		}
	} else if in.Protocol == "trojan" {
		return nil, fmt.Errorf("trojan inbound %q requires TLS or Reality stream settings", in.Tag)
	}

	return entry, nil
}

// resolveDNSServers returns the primary and secondary DNS addresses to embed
// in the Sing-box DNS block. Supports plain (default) and DoH modes via the
// `dns_mode` setting, with per-server overrides via `dns_primary`/`dns_secondary`.
func resolveDNSServers() (string, string) {
	mode := strings.ToLower(strings.TrimSpace(config.GetSetting("dns_mode")))
	primary := strings.TrimSpace(config.GetSetting("dns_primary"))
	secondary := strings.TrimSpace(config.GetSetting("dns_secondary"))

	if primary == "" {
		if mode == "doh" {
			primary = "https://cloudflare-dns.com/dns-query"
		} else {
			primary = "udp://8.8.8.8"
		}
	}
	if secondary == "" {
		if mode == "doh" {
			secondary = "https://dns.google/dns-query"
		} else {
			secondary = "udp://1.1.1.1"
		}
	}
	return primary, secondary
}

// applyStreamToSingbox extracts TLS and transport config from Xray-style stream
// settings and converts them to sing-box format.
func applyStreamToSingbox(entry map[string]interface{}, stream map[string]interface{}) {
	security, _ := stream["security"].(string)
	network, _ := stream["network"].(string)

	// TLS settings
	if security == "tls" {
		tls := map[string]interface{}{
			"enabled": true,
		}
		if tlsSettings, ok := stream["tlsSettings"].(map[string]interface{}); ok {
			if sn, ok := tlsSettings["serverName"].(string); ok && sn != "" {
				tls["server_name"] = sn
			}
			if certPath, ok := tlsSettings["certificateFile"].(string); ok && certPath != "" {
				tls["certificate_path"] = certPath
			}
			if keyPath, ok := tlsSettings["keyFile"].(string); ok && keyPath != "" {
				tls["key_path"] = keyPath
			}
			// Check certificates array (Xray format)
			if certs, ok := tlsSettings["certificates"].([]interface{}); ok && len(certs) > 0 {
				if cert, ok := certs[0].(map[string]interface{}); ok {
					if cp, ok := cert["certificateFile"].(string); ok && cp != "" {
						tls["certificate_path"] = cp
					}
					if kp, ok := cert["keyFile"].(string); ok && kp != "" {
						tls["key_path"] = kp
					}
				}
			}
			if alpn, ok := tlsSettings["alpn"].([]interface{}); ok {
				tls["alpn"] = alpn
			}
			// TLS fingerprint → uTLS block for browser-grade fingerprinting
			if fp, ok := tlsSettings["fingerprint"].(string); ok && fp != "" {
				tls["utls"] = map[string]interface{}{
					"enabled":     true,
					"fingerprint": fp,
				}
			}
		}
		entry["tls"] = tls
	} else if security == "reality" {
		tls := map[string]interface{}{
			"enabled": true,
		}
		reality := map[string]interface{}{}
		if rs, ok := stream["realitySettings"].(map[string]interface{}); ok {
			info := ReadRealityStreamInfo(stream)
			if pk, ok := rs["privateKey"].(string); ok && pk != "" {
				reality["private_key"] = pk
			}
			if len(info.ShortIDs) > 0 {
				reality["short_id"] = info.ShortIDs
			}
			if len(info.ServerNames) > 0 {
				tls["server_name"] = info.ServerNames[0]
			}
			// sing-box requires separate server and server_port for handshake
			if info.Target != "" {
				handshake := map[string]interface{}{}
				host, portStr, err := net.SplitHostPort(info.Target)
				if err == nil {
					handshake["server"] = host
					if p, e := strconv.Atoi(portStr); e == nil {
						handshake["server_port"] = p
					}
				} else {
					// No port in target, use as-is with default 443
					handshake["server"] = info.Target
					handshake["server_port"] = 443
				}
				reality["handshake"] = handshake
			}
		}
		tls["reality"] = reality
		entry["tls"] = tls
	}

	// Transport settings
	switch network {
	case "ws":
		transport := map[string]interface{}{
			"type": "ws",
		}
		if ws, ok := stream["wsSettings"].(map[string]interface{}); ok {
			if path, ok := ws["path"].(string); ok && path != "" {
				transport["path"] = path
			}
			if headers, ok := ws["headers"].(map[string]interface{}); ok {
				transport["headers"] = headers
			}
		}
		entry["transport"] = transport

	case "grpc":
		transport := map[string]interface{}{
			"type": "grpc",
		}
		if grpc, ok := stream["grpcSettings"].(map[string]interface{}); ok {
			if sn, ok := grpc["serviceName"].(string); ok && sn != "" {
				transport["service_name"] = sn
			}
		}
		entry["transport"] = transport

	case "h2", "http":
		transport := map[string]interface{}{
			"type": "http",
		}
		if h2, ok := stream["httpSettings"].(map[string]interface{}); ok {
			if path, ok := h2["path"].(string); ok && path != "" {
				transport["path"] = path
			}
			if host, ok := h2["host"].([]interface{}); ok {
				transport["host"] = host
			}
		}
		entry["transport"] = transport

	case "httpupgrade":
		transport := map[string]interface{}{
			"type": "httpupgrade",
		}
		if hu, ok := stream["httpupgradeSettings"].(map[string]interface{}); ok {
			if path, ok := hu["path"].(string); ok && path != "" {
				transport["path"] = path
			}
			if host, ok := hu["host"].(string); ok && host != "" {
				transport["host"] = host
			}
		}
		entry["transport"] = transport
	}
}

// buildSingboxRoutingRule converts a DB routing rule to sing-box route rule format.
func buildSingboxRoutingRule(r model.RoutingRule) map[string]interface{} {
	ruleMap := map[string]interface{}{
		"action":   "route",
		"outbound": r.OutboundTag,
	}

	hasContent := false

	if r.Domain != "" {
		domains := splitAndTrimCSV(r.Domain)
		if len(domains) > 0 {
			// Separate geosite references from domain suffixes
			var geosites, domainSuffixes []string
			for _, d := range domains {
				if strings.HasPrefix(d, "geosite:") {
					geosites = append(geosites, strings.TrimPrefix(d, "geosite:"))
				} else {
					domainSuffixes = append(domainSuffixes, d)
				}
			}
			if len(domainSuffixes) > 0 {
				ruleMap["domain_suffix"] = domainSuffixes
				hasContent = true
			}
			if len(geosites) > 0 {
				ruleMap["geosite"] = geosites
				hasContent = true
			}
		}
	}

	if r.IP != "" {
		ips := splitAndTrimCSV(r.IP)
		if len(ips) > 0 {
			// Separate geoip references from CIDR
			var geoips, cidrs []string
			for _, ip := range ips {
				if strings.HasPrefix(ip, "geoip:") {
					geoips = append(geoips, strings.TrimPrefix(ip, "geoip:"))
				} else {
					cidrs = append(cidrs, ip)
				}
			}
			if len(cidrs) > 0 {
				ruleMap["ip_cidr"] = cidrs
				hasContent = true
			}
			if len(geoips) > 0 {
				ruleMap["geoip"] = geoips
				hasContent = true
			}
		}
	}

	if r.Port != "" {
		ports := splitAndTrimCSV(r.Port)
		if len(ports) > 0 {
			// sing-box expects port as integers or port_range as strings
			var intPorts []int
			var portRanges []string
			for _, p := range ports {
				if strings.Contains(p, "-") {
					// Port range like "8443-9443" → use port_range
					portRanges = append(portRanges, p)
				} else if n, err := strconv.Atoi(p); err == nil {
					intPorts = append(intPorts, n)
				}
			}
			if len(intPorts) > 0 {
				ruleMap["port"] = intPorts
				hasContent = true
			}
			if len(portRanges) > 0 {
				ruleMap["port_range"] = portRanges
				hasContent = true
			}
		}
	}

	if !hasContent {
		return nil
	}
	return ruleMap
}

// buildSingboxOutbound converts a DB Outbound record to the sing-box JSON format.
// Returns nil for unknown/unsupported protocols (skipped silently).
func buildSingboxOutbound(ob model.Outbound) map[string]interface{} {
	switch ob.Protocol {
	case "wireguard":
		entry := map[string]interface{}{
			"type": "wireguard",
			"tag":  ob.Tag,
		}
		if ob.Config != "" {
			var cfg map[string]interface{}
			if err := json.Unmarshal([]byte(ob.Config), &cfg); err == nil {
				// WARPConfig fields → sing-box WireGuard fields
				if pk, ok := cfg["private_key"].(string); ok && pk != "" {
					entry["private_key"] = pk
				}
				endpoint := "engage.cloudflareclient.com"
				port := 2408
				if ep, ok := cfg["endpoint"].(string); ok && ep != "" {
					endpoint = ep
				}
				if p, ok := cfg["endpoint_port"].(float64); ok && p > 0 {
					port = int(p)
				}
				entry["server"] = endpoint
				entry["server_port"] = port
				if pubKey, ok := cfg["public_key"].(string); ok && pubKey != "" {
					entry["peer_public_key"] = pubKey
				}
				if addr, ok := cfg["address"].(string); ok && addr != "" {
					entry["local_address"] = []string{addr}
				}
				if rh, ok := cfg["reserved_hex"].(string); ok && rh != "" {
					entry["reserved"] = rh
				}
			}
		}
		return entry

	case "socks5":
		entry := map[string]interface{}{
			"type": "socks",
			"tag":  ob.Tag,
		}
		if ob.Config != "" {
			var cfg map[string]interface{}
			if err := json.Unmarshal([]byte(ob.Config), &cfg); err == nil {
				if server, ok := cfg["server"].(string); ok {
					entry["server"] = server
				}
				if port, ok := cfg["port"].(float64); ok {
					entry["server_port"] = int(port)
				}
				if user, ok := cfg["username"].(string); ok && user != "" {
					entry["username"] = user
				}
				if pw, ok := cfg["password"].(string); ok && pw != "" {
					entry["password"] = pw
				}
			}
		}
		return entry

	case "http":
		entry := map[string]interface{}{
			"type": "http",
			"tag":  ob.Tag,
		}
		if ob.Config != "" {
			var cfg map[string]interface{}
			if err := json.Unmarshal([]byte(ob.Config), &cfg); err == nil {
				if server, ok := cfg["server"].(string); ok {
					entry["server"] = server
				}
				if port, ok := cfg["port"].(float64); ok {
					entry["server_port"] = int(port)
				}
			}
		}
		return entry
	}
	return nil
}

func (s *SingboxManager) Start() error {
	if s.Status() {
		return fmt.Errorf("sing-box is already running")
	}

	cfgJSON, err := s.GenerateConfig()
	if err != nil {
		return err
	}
	if err := WriteConfigToFile(s.ConfigPath, cfgJSON); err != nil {
		return err
	}

	cmd := exec.Command(s.BinaryPath, "run", "-c", s.ConfigPath)
	return s.startAndVerify(cmd)
}

func (s *SingboxManager) Restart() error {
	if err := s.Stop(); err != nil {
		return err
	}
	return s.Start()
}
