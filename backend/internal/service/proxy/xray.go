package proxy

import (
	"fmt"
	"os/exec"

	"github.com/harveyxiacn/ZenithPanel/backend/internal/config"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/model"
)

type XrayManager struct {
	BaseCore
}

func NewXrayManager() *XrayManager {
	return &XrayManager{
		BaseCore: BaseCore{
			BinaryPath: "xray", // Or absolute path to xray binary
			ConfigPath: "xray_config.json", // Path to save generated config
		},
	}
}

func (x *XrayManager) GenerateConfig() (string, error) {
	var inbounds []model.Inbound
	var rules []model.RoutingRule

	// Fetch active configurations from DB
	config.DB.Where("enable = ?", true).Find(&inbounds)
	config.DB.Where("enable = ?", true).Find(&rules)

	// Build the JSON structure for Xray
	// This is a minimal skeleton
	xrayConfig := map[string]interface{}{
		"log": map[string]interface{}{
			"loglevel": "warning",
		},
		"inbounds":  []map[string]interface{}{},
		"outbounds": []map[string]interface{}{
			{
				"protocol": "freedom",
				"tag":      "direct",
			},
			{
				"protocol": "blackhole",
				"tag":      "block",
			},
		},
		"routing": map[string]interface{}{
			"domainStrategy": "AsIs",
			"rules":          []map[string]interface{}{},
		},
	}

	// Process Inbounds
	for _, in := range inbounds {
		inboundMap := map[string]interface{}{
			"tag":      in.Tag,
			"port":     in.Port,
			"protocol": in.Protocol,
		}
		// Typically we would unmarshal `in.Settings` and `in.Stream` and merge them
		// For the skeleton, we just append
		inboundList := xrayConfig["inbounds"].([]map[string]interface{})
		xrayConfig["inbounds"] = append(inboundList, inboundMap)
	}

	// Process Routing Rules
	for _, r := range rules {
		ruleMap := map[string]interface{}{
			"type":        "field",
			"outboundTag": r.OutboundTag,
		}
		if r.Domain != "" {
			ruleMap["domain"] = []string{r.Domain}
		}
		if r.IP != "" {
			ruleMap["ip"] = []string{r.IP}
		}
		ruleList := xrayConfig["routing"].(map[string]interface{})["rules"].([]map[string]interface{})
		xrayConfig["routing"].(map[string]interface{})["rules"] = append(ruleList, ruleMap)
	}

	return PrettifyJSON(xrayConfig)
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

	x.cmd = exec.Command(x.BinaryPath, "run", "-c", x.ConfigPath)
	return x.cmd.Start()
}

func (x *XrayManager) Restart() error {
	x.Stop()
	return x.Start()
}
