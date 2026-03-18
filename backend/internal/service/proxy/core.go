package proxy

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"sync"
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
	return os.WriteFile(path, []byte(content), 0600)
}

// BaseCore provides common functionalities for proxy cores
type BaseCore struct {
	BinaryPath string
	ConfigPath string
	mu         sync.RWMutex
	cmd        *exec.Cmd
}

func (c *BaseCore) Status() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.cmd != nil
}

func (c *BaseCore) setCmd(cmd *exec.Cmd) {
	c.mu.Lock()
	c.cmd = cmd
	c.mu.Unlock()
}

func (c *BaseCore) clearCmd(cmd *exec.Cmd) {
	c.mu.Lock()
	if c.cmd == cmd {
		c.cmd = nil
	}
	c.mu.Unlock()
}

func (c *BaseCore) trackCmd(cmd *exec.Cmd) {
	go func() {
		_ = cmd.Wait()
		c.clearCmd(cmd)
	}()
}

func (c *BaseCore) Stop() error {
	c.mu.Lock()
	cmd := c.cmd
	c.cmd = nil
	c.mu.Unlock()

	if cmd == nil || cmd.Process == nil {
		return nil
	}

	if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
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
