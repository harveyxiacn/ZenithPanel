package proxy

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"time"
)

// CoreManager defines the interface for managing a proxy core
type CoreManager interface {
	GenerateConfig() (string, error)
	Start() error
	Stop() error
	Restart() error
	Status() bool
	LastError() string
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
	lastErr    string
	stderrBuf  bytes.Buffer
}

func (c *BaseCore) Status() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.cmd != nil
}

// LastError returns the last stderr output from the proxy process (useful when it crashes).
func (c *BaseCore) LastError() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.lastErr
}

func (c *BaseCore) setCmd(cmd *exec.Cmd) {
	c.mu.Lock()
	c.cmd = cmd
	c.mu.Unlock()
}

func (c *BaseCore) clearCmd(cmd *exec.Cmd) {
	c.mu.Lock()
	if c.cmd == cmd {
		c.lastErr = c.stderrBuf.String()
		c.cmd = nil
	}
	c.mu.Unlock()
}

// startAndVerify starts the process, captures stderr, and waits briefly to
// detect early crashes (e.g., config parse errors). Returns an error if the
// process exits within the first second.
func (c *BaseCore) startAndVerify(cmd *exec.Cmd) error {
	c.stderrBuf.Reset()
	cmd.Stderr = &c.stderrBuf

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start %s: %w", c.BinaryPath, err)
	}
	c.setCmd(cmd)

	// Single goroutine owns cmd.Wait(); signals via channel for early crash detection.
	exited := make(chan error, 1)
	go func() {
		err := cmd.Wait()
		exited <- err
		c.clearCmd(cmd)
	}()

	select {
	case err := <-exited:
		// Process exited within the grace period — config or runtime error
		stderr := c.stderrBuf.String()
		if len(stderr) > 500 {
			stderr = stderr[len(stderr)-500:]
		}
		if stderr == "" {
			stderr = "process exited with no output"
		}
		return fmt.Errorf("%s crashed on startup: %v\n%s", c.BinaryPath, err, stderr)
	case <-time.After(1500 * time.Millisecond):
		// Process still running after 1.5s — likely started successfully.
		// The goroutine above will call clearCmd when it eventually exits.
		return nil
	}
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
