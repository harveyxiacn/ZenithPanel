package proxy

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
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

// ringBuffer is a fixed-size buffer that keeps the most recent N bytes written
// to it. It implements io.Writer so it can be used as cmd.Stdout/Stderr.
// Used by BaseCore to capture proxy logs without unbounded memory growth.
type ringBuffer struct {
	mu   sync.Mutex
	buf  []byte
	size int
	full bool
	head int
}

func newRingBuffer(size int) *ringBuffer {
	return &ringBuffer{buf: make([]byte, size), size: size}
}

func (r *ringBuffer) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := len(p)
	if n == 0 {
		return 0, nil
	}
	// Only keep the trailing "size" bytes if the incoming chunk is larger.
	if n >= r.size {
		copy(r.buf, p[n-r.size:])
		r.head = 0
		r.full = true
		return n, nil
	}
	first := copy(r.buf[r.head:], p)
	if first < n {
		copy(r.buf, p[first:])
		r.full = true
		r.head = n - first
	} else {
		r.head = (r.head + n) % r.size
		if r.head == 0 {
			r.full = true
		}
	}
	return n, nil
}

func (r *ringBuffer) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.full {
		return string(r.buf[:r.head])
	}
	out := make([]byte, 0, r.size)
	out = append(out, r.buf[r.head:]...)
	out = append(out, r.buf[:r.head]...)
	return string(out)
}

func (r *ringBuffer) Reset() {
	r.mu.Lock()
	r.head = 0
	r.full = false
	r.mu.Unlock()
}

// BaseCore provides common functionalities for proxy cores.
// outputBuf captures stdout+stderr in a ring buffer so long-running engines
// can report their most recent log output without leaking memory.
//
// dualMode is consulted by GenerateConfig on each manager to decide whether
// the engine should serve every enabled inbound (legacy single-engine mode)
// or only the subset that the other engine cannot handle (dual mode, where
// Xray and Sing-box run side-by-side and each takes a disjoint partition).
type BaseCore struct {
	BinaryPath string
	ConfigPath string
	mu         sync.RWMutex
	cmd        *exec.Cmd
	lastErr    string
	outputBuf  *ringBuffer
	dualMode   bool
}

// SetDualMode toggles dual-engine partitioning. Call this before Restart()/Start()
// so the next config generation honors the new mode.
func (c *BaseCore) SetDualMode(b bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.dualMode = b
}

// IsDualMode reports whether dual-engine partitioning is enabled. Returned
// under the read lock so it's safe to call from config generators.
func (c *BaseCore) IsDualMode() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.dualMode
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
		if c.outputBuf != nil {
			c.lastErr = c.outputBuf.String()
		}
		c.cmd = nil
	}
	c.mu.Unlock()
}

// validateConfig runs the engine's built-in config check if available.
// sing-box supports "check -c", xray does not have a validation subcommand.
// If validation is not available, we skip it and rely on crash detection.
func (c *BaseCore) validateConfig() error {
	if c.BinaryPath != "sing-box" {
		// Xray has no config validation command — skip, rely on crash detection
		return nil
	}
	cmd := exec.Command(c.BinaryPath, "check", "-c", c.ConfigPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		output := string(out)
		if len(output) > 800 {
			output = output[len(output)-800:]
		}
		if output == "" {
			output = err.Error()
		}
		hint := annotateSingboxError(c.ConfigPath, output)
		if hint != "" {
			return fmt.Errorf("config validation failed:\n%s\n\n%s", output, hint)
		}
		return fmt.Errorf("config validation failed:\n%s", output)
	}
	return nil
}

// annotateSingboxError post-processes a failed `sing-box check` output to add
// a human-readable hint that maps inbound[N] back to the inbound's tag, and
// flags missing cert files on disk. sing-box's native error references the
// inbound by ordinal, which is useless when the panel has half a dozen of
// them — this surfaces the actual tag the operator needs to fix.
func annotateSingboxError(configPath, output string) string {
	idx, ok := parseInboundIndex(output)
	if !ok {
		return ""
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		return ""
	}
	var cfg struct {
		Inbounds []map[string]any `json:"inbounds"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return ""
	}
	if idx < 0 || idx >= len(cfg.Inbounds) {
		return ""
	}
	ib := cfg.Inbounds[idx]
	tag, _ := ib["tag"].(string)
	typ, _ := ib["type"].(string)
	hint := fmt.Sprintf("Hint: the failing inbound is index %d — tag=%q, type=%q.", idx, tag, typ)
	if tls, ok := ib["tls"].(map[string]any); ok {
		if cp, _ := tls["certificate_path"].(string); cp != "" {
			if _, err := os.Stat(cp); err != nil {
				hint += fmt.Sprintf("\nCert file %q does not exist on disk — issue or upload the certificate, or switch the inbound to Reality.", cp)
			}
		}
		if kp, _ := tls["key_path"].(string); kp != "" {
			if _, err := os.Stat(kp); err != nil {
				hint += fmt.Sprintf("\nKey file %q does not exist on disk.", kp)
			}
		}
	}
	return hint
}

// parseInboundIndex extracts N from sing-box error strings like
// "initialize inbound[3]: missing certificate". Returns (idx, true) on match.
func parseInboundIndex(output string) (int, bool) {
	const marker = "inbound["
	i := strings.Index(output, marker)
	if i < 0 {
		return 0, false
	}
	rest := output[i+len(marker):]
	end := strings.IndexByte(rest, ']')
	if end <= 0 {
		return 0, false
	}
	n, err := strconv.Atoi(rest[:end])
	if err != nil {
		return 0, false
	}
	return n, true
}

// startAndVerify validates the config, starts the process, captures stdout+stderr,
// and waits briefly to detect early crashes.
func (c *BaseCore) startAndVerify(cmd *exec.Cmd) error {
	// Validate config before starting — catches most errors immediately
	if err := c.validateConfig(); err != nil {
		return fmt.Errorf("%s %v", c.BinaryPath, err)
	}

	if c.outputBuf == nil {
		c.outputBuf = newRingBuffer(8 * 1024)
	} else {
		c.outputBuf.Reset()
	}
	// Capture both stdout and stderr — xray writes errors to stdout
	cmd.Stdout = c.outputBuf
	cmd.Stderr = c.outputBuf

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
		output := c.outputBuf.String()
		if len(output) > 800 {
			output = output[len(output)-800:]
		}
		if output == "" {
			output = "process exited with no output"
		}
		return fmt.Errorf("%s crashed on startup: %v\n%s", c.BinaryPath, err, output)
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
func PrettifyJSON(v any) (string, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}
