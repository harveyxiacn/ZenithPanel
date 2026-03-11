package proxy

import (
	"encoding/json"
	"os"
	"os/exec"
)

// CoreManager defines the interface for managing a proxy core
type CoreManager interface {
	GenerateConfig() (string, error)
	Start() error
	Stop() error
	Restart() error
	Status() bool
}

// WriteConfigToFile writes the given configuration string to a file
func WriteConfigToFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0644)
}

// BaseCore provides common functionalities for proxy cores
type BaseCore struct {
	BinaryPath string
	ConfigPath string
	cmd        *exec.Cmd
}

func (c *BaseCore) Status() bool {
	return c.cmd != nil && c.cmd.Process != nil && c.cmd.ProcessState == nil
}

func (c *BaseCore) Stop() error {
	if c.Status() {
		return c.cmd.Process.Kill()
	}
	return nil
}

// PrettifyJSON takes any object and returns an indented JSON string
func PrettifyJSON(v interface{}) (string, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}
