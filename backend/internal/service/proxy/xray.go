package proxy

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/harveyxiacn/ZenithPanel/backend/internal/config"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/model"
)

type XrayManager struct {
	BaseCore
}

func NewXrayManager() *XrayManager {
	return &XrayManager{
		BaseCore: BaseCore{
			BinaryPath: "xray",
			ConfigPath: "data/xray_config.json",
		},
	}
}

func (x *XrayManager) GenerateConfig() (string, error) {
	var inbounds []model.Inbound
	var rules []model.RoutingRule

	config.DB.Where("enable = ?", true).Find(&inbounds)
	config.DB.Where("enable = ?", true).Find(&rules)

	xrayConfig := map[string]interface{}{
		"log": map[string]interface{}{
			"loglevel": "warning",
		},
		"inbounds": []interface{}{},
		"outbounds": []interface{}{
			map[string]interface{}{
				"protocol": "freedom",
				"tag":      "direct",
			},
			map[string]interface{}{
				"protocol": "blackhole",
				"tag":      "block",
			},
		},
		"routing": map[string]interface{}{
			"domainStrategy": "AsIs",
			"rules":          []interface{}{},
		},
	}

	for _, in := range inbounds {
		inboundEntry, err := buildXrayInbound(in)
		if err != nil {
			return "", fmt.Errorf("build inbound %q: %w", in.Tag, err)
		}
		xrayConfig["inbounds"] = append(xrayConfig["inbounds"].([]interface{}), inboundEntry)
	}

	for _, r := range rules {
		ruleMap := buildXrayRoutingRule(r)
		xrayConfig["routing"].(map[string]interface{})["rules"] = append(
			xrayConfig["routing"].(map[string]interface{})["rules"].([]interface{}), ruleMap,
		)
	}

	return PrettifyJSON(xrayConfig)
}

// buildXrayInbound constructs a complete Xray inbound entry from the DB model,
// parsing the Settings/Stream JSON and injecting clients from the Client table.
func buildXrayInbound(in model.Inbound) (map[string]interface{}, error) {
	listen := in.Listen
	if listen == "" {
		listen = "0.0.0.0"
	}
	entry := map[string]interface{}{
		"tag":      in.Tag,
		"port":     in.Port,
		"listen":   listen,
		"protocol": in.Protocol,
	}

	// Parse settings JSON
	settings := map[string]interface{}{}
	if in.Settings != "" && in.Settings != "{}" {
		if err := json.Unmarshal([]byte(in.Settings), &settings); err != nil {
			return nil, fmt.Errorf("parse settings: %w", err)
		}
	}

	// Fetch clients for this inbound and inject them
	var clients []model.Client
	config.DB.Where("inbound_id = ? AND enable = ?", in.ID, true).Find(&clients)

	switch in.Protocol {
	case "vless":
		if _, ok := settings["decryption"]; !ok {
			settings["decryption"] = "none"
		}
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
		delete(settings, "flow") // flow belongs on client, not top-level

	case "vmess":
		clientList := []map[string]interface{}{}
		for _, c := range clients {
			clientList = append(clientList, map[string]interface{}{
				"id":      c.UUID,
				"email":   c.Email,
				"alterId": 0,
			})
		}
		settings["clients"] = clientList

	case "trojan":
		clientList := []map[string]interface{}{}
		for _, c := range clients {
			clientList = append(clientList, map[string]interface{}{
				"password": c.UUID,
				"email":    c.Email,
			})
		}
		settings["clients"] = clientList

	case "shadowsocks":
		// Shadowsocks uses a single password, clients share it or use AEAD 2022 multi-user
		// Keep existing settings (method, password) as-is

	case "hysteria2":
		clientList := []map[string]interface{}{}
		for _, c := range clients {
			clientList = append(clientList, map[string]interface{}{
				"password": c.UUID,
				"email":    c.Email,
			})
		}
		settings["clients"] = clientList
	}

	entry["settings"] = settings

	// Parse stream settings JSON
	if in.Stream != "" && in.Stream != "{}" {
		streamSettings := map[string]interface{}{}
		if err := json.Unmarshal([]byte(in.Stream), &streamSettings); err != nil {
			return nil, fmt.Errorf("parse stream: %w", err)
		}
		entry["streamSettings"] = streamSettings
	}

	return entry, nil
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
	if err := cmd.Start(); err != nil {
		return err
	}
	x.setCmd(cmd)
	x.trackCmd(cmd)
	return nil
}

func (x *XrayManager) Restart() error {
	if err := x.Stop(); err != nil {
		return err
	}
	return x.Start()
}
