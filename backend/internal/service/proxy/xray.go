package proxy

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"github.com/harveyxiacn/ZenithPanel/backend/internal/config"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/model"
)

type XrayManager struct {
	BaseCore
	skippedProtos []string
}

func NewXrayManager() *XrayManager {
	return &XrayManager{
		BaseCore: BaseCore{
			BinaryPath: "xray",
			ConfigPath: "data/xray_config.json",
		},
	}
}

// xraySupportedProtocols lists protocols supported by Xray-core.
// Hysteria2, WireGuard etc. are sing-box only and must be skipped.
var xraySupportedProtocols = map[string]bool{
	"vless":       true,
	"vmess":       true,
	"trojan":      true,
	"shadowsocks": true,
}

// SkippedProtocols returns protocol names that were skipped during the last config generation
// because they are not supported by the engine.
func (x *XrayManager) SkippedProtocols() []string {
	x.mu.RLock()
	defer x.mu.RUnlock()
	return x.skippedProtos
}

// XrayStatsAPIPort returns the loopback port Xray exposes its StatsService on.
// The traffic accountant exec-polls this via "xray api statsquery" to populate
// per-client UpLoad/DownLoad. Configurable via xray_stats_port setting so we
// don't collide with whatever else might already bind 10085.
func XrayStatsAPIPort() int {
	if raw := config.GetSetting("xray_stats_port"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 && n < 65536 {
			return n
		}
	}
	return 10085
}

func (x *XrayManager) GenerateConfig() (string, error) {
	var inbounds []model.Inbound
	var rules []model.RoutingRule

	config.DB.Where("enable = ?", true).Find(&inbounds)
	config.DB.Where("enable = ?", true).Find(&rules)
	rules = UniqueRoutingRules(rules)

	// Batch-load every enabled client for the active inbounds in one query,
	// then group by inbound_id. Avoids issuing one query per inbound inside the loop.
	clientsByInbound := fetchClientsByInbound(inbounds)

	xrayPrimary, xraySecondary := resolveDNSServers()
	// Xray's DNS expects bare server addresses (IPs, FQDNs, or https:// URLs for DoH).
	// Strip the "udp://" prefix Sing-box uses for plain DNS so Xray parses correctly.
	xrayPrimary = strings.TrimPrefix(xrayPrimary, "udp://")
	xraySecondary = strings.TrimPrefix(xraySecondary, "udp://")
	statsPort := XrayStatsAPIPort()
	xrayConfig := map[string]interface{}{
		"log": map[string]interface{}{
			"loglevel": "warning",
		},
		// Enable per-user uplink/downlink counters. The traffic accountant
		// reads these via the api inbound below; without this block Xray
		// emits no stats and the panel's UpLoad/DownLoad columns stay zero.
		"stats": map[string]interface{}{},
		"policy": map[string]interface{}{
			"levels": map[string]interface{}{
				"0": map[string]interface{}{
					"statsUserUplink":   true,
					"statsUserDownlink": true,
				},
			},
		},
		"api": map[string]interface{}{
			"tag":      "api",
			"services": []string{"StatsService", "HandlerService"},
		},
		"dns": map[string]interface{}{
			"servers": []interface{}{
				xrayPrimary,
				xraySecondary,
				"localhost",
			},
		},
		"inbounds": []interface{}{
			// Loopback-only API inbound for stats polling. dokodemo-door
			// followType "api" is Xray's recommended way to expose StatsService
			// to in-host consumers.
			map[string]interface{}{
				"tag":      "api",
				"listen":   "127.0.0.1",
				"port":     statsPort,
				"protocol": "dokodemo-door",
				"settings": map[string]interface{}{
					"address": "127.0.0.1",
				},
			},
		},
		"outbounds": []interface{}{
			map[string]interface{}{
				"protocol": "freedom",
				"tag":      "direct",
				"settings": map[string]interface{}{
					"domainStrategy": "UseIPv4v6",
				},
			},
			map[string]interface{}{
				"protocol": "blackhole",
				"tag":      "block",
			},
		},
		"routing": map[string]interface{}{
			"domainStrategy": "IPIfNonMatch",
			"rules": []interface{}{
				// Pin the api inbound to the api outbound so stats traffic
				// never leaks through user-defined routing rules.
				map[string]interface{}{
					"type":        "field",
					"inboundTag":  []string{"api"},
					"outboundTag": "api",
				},
			},
		},
	}

	var skipped []string
	for _, in := range inbounds {
		if !xraySupportedProtocols[in.Protocol] {
			skipped = append(skipped, fmt.Sprintf("%s (%s)", in.Tag, in.Protocol))
			continue
		}
		inboundEntry, err := buildXrayInbound(in, clientsByInbound[in.ID])
		if err != nil {
			return "", fmt.Errorf("build inbound %q: %w", in.Tag, err)
		}
		xrayConfig["inbounds"] = append(xrayConfig["inbounds"].([]interface{}), inboundEntry)
	}
	x.mu.Lock()
	x.skippedProtos = skipped
	x.mu.Unlock()

	for _, r := range rules {
		ruleMap := buildXrayRoutingRule(r)
		xrayConfig["routing"].(map[string]interface{})["rules"] = append(
			xrayConfig["routing"].(map[string]interface{})["rules"].([]interface{}), ruleMap,
		)
	}

	return PrettifyJSON(xrayConfig)
}

// buildXrayInbound constructs a complete Xray inbound entry from the DB model,
// parsing the Settings/Stream JSON and injecting the supplied client list.
func buildXrayInbound(in model.Inbound, clients []model.Client) (map[string]interface{}, error) {
	entry := map[string]interface{}{
		"tag":      in.Tag,
		"port":     in.Port,
		"protocol": in.Protocol,
		"sniffing": map[string]interface{}{
			"enabled":      true,
			"destOverride": []string{"http", "tls", "quic", "fakedns"},
		},
	}
	// Only set listen if explicitly configured; omitting lets Xray listen
	// on both IPv4 and IPv6 (dual-stack).
	if in.Listen != "" {
		entry["listen"] = in.Listen
	}

	// Parse settings JSON
	settings := map[string]interface{}{}
	if in.Settings != "" && in.Settings != "{}" {
		if err := json.Unmarshal([]byte(in.Settings), &settings); err != nil {
			return nil, fmt.Errorf("parse settings: %w", err)
		}
	}

	switch in.Protocol {
	case "vless":
		if _, ok := settings["decryption"]; !ok {
			settings["decryption"] = "none"
		}
		if len(clients) > 0 {
			clientList := []map[string]interface{}{}
			for _, c := range clients {
				cm := map[string]interface{}{"id": c.UUID, "email": c.Email}
				// Preserve flow if set in settings template
				if flow, ok := settings["flow"]; ok {
					cm["flow"] = flow
				}
				clientList = append(clientList, cm)
			}
			settings["clients"] = clientList
		}
		delete(settings, "flow") // flow belongs on client, not top-level

	case "vmess":
		if len(clients) > 0 {
			clientList := []map[string]interface{}{}
			for _, c := range clients {
				clientList = append(clientList, map[string]interface{}{
					"id":      c.UUID,
					"email":   c.Email,
					"alterId": 0,
				})
			}
			settings["clients"] = clientList
		}

	case "trojan":
		if len(clients) > 0 {
			clientList := []map[string]interface{}{}
			for _, c := range clients {
				clientList = append(clientList, map[string]interface{}{
					"password": c.UUID,
					"email":    c.Email,
				})
			}
			settings["clients"] = clientList
		}

	case "shadowsocks":
		// AEAD-2022 methods support per-user multi-user mode in Xray.
		// Classic methods use a single shared password; keep settings as-is.
		if method, ok := settings["method"].(string); ok && strings.HasPrefix(method, "2022-blake3") {
			if len(clients) > 0 {
				clientList := []map[string]interface{}{}
				for _, c := range clients {
					clientList = append(clientList, map[string]interface{}{
						"password": c.UUID,
						"email":    c.Email,
					})
				}
				settings["clients"] = clientList
			}
		}
	}

	entry["settings"] = settings

	// Parse stream settings JSON
	if in.Stream != "" && in.Stream != "{}" {
		streamSettings := map[string]interface{}{}
		if err := json.Unmarshal([]byte(in.Stream), &streamSettings); err != nil {
			return nil, fmt.Errorf("parse stream: %w", err)
		}
		entry["streamSettings"] = NormalizeXrayStreamSettings(streamSettings)
	}

	return entry, nil
}

// fetchClientsByInbound loads all enabled clients whose inbound_id is in the
// supplied list with a single query and groups them by inbound ID.
// Replaces the previous per-inbound query (N+1) during config generation.
func fetchClientsByInbound(inbounds []model.Inbound) map[uint][]model.Client {
	grouped := make(map[uint][]model.Client, len(inbounds))
	if len(inbounds) == 0 {
		return grouped
	}
	ids := make([]uint, 0, len(inbounds))
	for _, in := range inbounds {
		ids = append(ids, in.ID)
	}
	var clients []model.Client
	config.DB.Where("inbound_id IN ? AND enable = ?", ids, true).Find(&clients)
	for _, c := range clients {
		grouped[c.InboundID] = append(grouped[c.InboundID], c)
	}
	return grouped
}

func splitAndTrimCSV(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func buildXrayRoutingRule(r model.RoutingRule) map[string]interface{} {
	ruleMap := map[string]interface{}{
		"type":        "field",
		"outboundTag": r.OutboundTag,
	}

	if domains := splitAndTrimCSV(r.Domain); len(domains) > 0 {
		ruleMap["domain"] = domains
	}
	if ips := splitAndTrimCSV(r.IP); len(ips) > 0 {
		ruleMap["ip"] = ips
	}
	if ports := splitAndTrimCSV(r.Port); len(ports) > 0 {
		ruleMap["port"] = strings.Join(ports, ",")
	}

	return ruleMap
}

func (x *XrayManager) Start() error {
	if x.Status() {
		return fmt.Errorf("xray is already running")
	}

	cfgJSON, err := x.GenerateConfig()
	if err != nil {
		return err
	}
	if err := WriteConfigToFile(x.ConfigPath, cfgJSON); err != nil {
		return err
	}

	cmd := exec.Command(x.BinaryPath, "run", "-c", x.ConfigPath)
	return x.startAndVerify(cmd)
}

func (x *XrayManager) Restart() error {
	if err := x.Stop(); err != nil {
		return err
	}
	return x.Start()
}
