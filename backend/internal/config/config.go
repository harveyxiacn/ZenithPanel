package config

import (
	"sync"
)

// GlobalConfig holds the runtime configuration state
type GlobalConfig struct {
	IsSetupComplete   bool
	SetupURLSuffix    string
	SetupOneTimeToken string
	PanelPrefix       string
}

var (
	Instance *GlobalConfig
	once     sync.Once
)

// GetConfig returns the singleton GlobalConfig
func GetConfig() *GlobalConfig {
	once.Do(func() {
		Instance = &GlobalConfig{
			IsSetupComplete: false,
			PanelPrefix:     "/", // Default before custom path is set
		}
	})
	return Instance
}
