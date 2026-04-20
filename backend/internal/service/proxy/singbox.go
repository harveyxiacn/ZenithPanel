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

	config.DB.Where("enable = ?", true).Find(&inbounds)
	config.DB.Where("enable = ?", true).Find(&rules)
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

	singboxConfig := map[string]interface{}{
		"log": map[string]interface{}{
			"level": "warn",
		},
		"dns": map[string]interface{}{
			"servers": []interface{}{
				map[string]interface{}{
					"tag":     "dns-remote",
					"address": "udp://8.8.8.8",
				},
				map[string]interface{}{
					"tag":     "dns-google",
					"address": "udp://1.1.1.1",
				},
				map[string]interface{}{
					"tag":     "dns-local",
					"address": "local",
				},
			},
			"strategy": "prefer_ipv4",
			"final":    "dns-remote",
		},
		"inbounds": []interface{}{},
		"outbounds": []interface{}{
			map[string]interface{}{
				"type": "direct",
				"tag":  "direct",
			},
			map[string]interface{}{
				"type": "block",
				"tag":  "block",
			},
			map[string]interface{}{
				"type": "dns",
				"tag":  "dns-out",
			},
		},
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
		// Shadowsocks uses method + password from settings
		if settingsRaw != nil {
			if method, ok := settingsRaw["method"].(string); ok {
				entry["method"] = method
			}
			if password, ok := settingsRaw["password"].(string); ok {
				entry["password"] = password
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
		applyStreamToSingbox(entry, stream)
	}

	return entry, nil
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
