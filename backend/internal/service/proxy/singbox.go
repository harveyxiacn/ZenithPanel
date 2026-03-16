package proxy

import (
	"fmt"
	"os/exec"

	"github.com/harveyxiacn/ZenithPanel/backend/internal/config"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/model"
)

type SingboxManager struct {
	BaseCore
}

func NewSingboxManager() *SingboxManager {
	return &SingboxManager{
		BaseCore: BaseCore{
			BinaryPath: "sing-box",       // Or absolute path to sing-box binary
			ConfigPath: "data/singbox_config.json",
		},
	}
}

func (s *SingboxManager) GenerateConfig() (string, error) {
	var inbounds []model.Inbound
	var rules []model.RoutingRule

	config.DB.Where("enable = ?", true).Find(&inbounds)
	config.DB.Where("enable = ?", true).Find(&rules)

	// Build the JSON structure for Sing-box
	singboxConfig := map[string]interface{}{
		"log": map[string]interface{}{
			"level": "warn",
		},
		"inbounds":  []map[string]interface{}{},
		"outbounds": []map[string]interface{}{
			{
				"type": "direct",
				"tag":  "direct",
			},
			{
				"type": "block",
				"tag":  "block",
			},
		},
		"route": map[string]interface{}{
			"rules": []map[string]interface{}{},
		},
	}

	// Translate DB models to Sing-box format
	for _, in := range inbounds {
		inboundMap := map[string]interface{}{
			"type":       in.Protocol,
			"tag":        in.Tag,
			"listen":     "::",
			"listen_port": in.Port,
		}
		inboundList := singboxConfig["inbounds"].([]map[string]interface{})
		singboxConfig["inbounds"] = append(inboundList, inboundMap)
	}

	for _, r := range rules {
		ruleMap := map[string]interface{}{
			"outbound": r.OutboundTag,
		}
		if r.Domain != "" {
			ruleMap["domain_suffix"] = []string{r.Domain}
		}
		if r.IP != "" {
			ruleMap["ip_cidr"] = []string{r.IP}
		}
		ruleList := singboxConfig["route"].(map[string]interface{})["rules"].([]map[string]interface{})
		singboxConfig["route"].(map[string]interface{})["rules"] = append(ruleList, ruleMap)
	}

	return PrettifyJSON(singboxConfig)
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

	s.cmd = exec.Command(s.BinaryPath, "run", "-c", s.ConfigPath)
	return s.cmd.Start()
}

func (s *SingboxManager) Restart() error {
	s.Stop()
	return s.Start()
}
