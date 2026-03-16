package terminal

import (
	"encoding/json"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"

	"github.com/creack/pty"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	// Only allow WebSocket connections from the same host (prevents Cross-Site WebSocket Hijacking).
	// Uses strict URL parsing instead of substring matching to prevent bypass.
	CheckOrigin: func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		if origin == "" {
			return true // non-browser clients (curl, proxy apps)
		}
		parsed, err := url.Parse(origin)
		if err != nil {
			return false
		}
		originHost := parsed.Hostname()
		reqHost := r.Host
		if h, _, err := net.SplitHostPort(reqHost); err == nil {
			reqHost = h
		}
		return originHost == reqHost
	},
}

// wsMsg is the JSON protocol for terminal WebSocket messages.
type wsMsg struct {
	Type string `json:"type"` // "cmd", "resize", "heartbeat"
	Data string `json:"data"`
	Cols int    `json:"cols"`
	Rows int    `json:"rows"`
}

// buildShellCmd returns a command that opens a host shell via nsenter (if running
// in a container with --pid=host), or falls back to a local container shell.
func buildShellCmd() *exec.Cmd {
	// Try nsenter into host PID 1 — works when container has --pid=host + --privileged
	if _, err := os.Stat("/proc/1/ns/mnt"); err == nil {
		if nsenter, err := exec.LookPath("nsenter"); err == nil {
			// Find best shell on host
			hostShell := "/bin/sh"
			for _, sh := range []string{"/bin/bash", "/bin/sh"} {
				// Check if shell exists on host via nsenter
				check := exec.Command(nsenter, "-t", "1", "-m", "--", "test", "-x", sh)
				if check.Run() == nil {
					hostShell = sh
					break
				}
			}
			cmd := exec.Command(nsenter, "--mount", "--uts", "--ipc", "--net", "--pid", "--target", "1", "--", hostShell, "--login")
			cmd.Env = []string{"TERM=xterm-256color", "HOME=/root", "PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"}
			return cmd
		}
	}

	// Fallback: local container shell
	shell := "/bin/sh"
	for _, sh := range []string{"/bin/bash", "/bin/sh"} {
		if _, err := os.Stat(sh); err == nil {
			shell = sh
			break
		}
	}
	cmd := exec.Command(shell)
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	if home, err := os.UserHomeDir(); err == nil {
		cmd.Dir = home
	}
	return cmd
}

// HandleTerminalWebSocket upgrades the HTTP request to a WebSocket and spawns
// a host shell via nsenter+PTY — no SSH credentials required.
func HandleTerminalWebSocket(c *gin.Context) {
	ws, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		log.Printf("Failed to upgrade websocket: %v", err)
		return
	}
	defer ws.Close()

	cmd := buildShellCmd()

	ptmx, err := pty.Start(cmd)
	if err != nil {
		ws.WriteMessage(websocket.TextMessage, []byte("\r\n\x1b[31mFailed to start shell: "+err.Error()+"\x1b[0m\r\n"))
		return
	}
	defer func() {
		ptmx.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	// PTY output → WebSocket
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := ptmx.Read(buf)
			if err != nil {
				ws.WriteMessage(websocket.TextMessage, []byte("\r\n\x1b[31m[Session ended]\x1b[0m\r\n"))
				ws.Close()
				return
			}
			ws.WriteMessage(websocket.BinaryMessage, buf[:n])
		}
	}()

	// WebSocket → PTY stdin
	for {
		_, p, err := ws.ReadMessage()
		if err != nil {
			break
		}

		// Try to parse as JSON message for resize/heartbeat
		var msg wsMsg
		if json.Unmarshal(p, &msg) == nil && msg.Type != "" {
			switch msg.Type {
			case "resize":
				if msg.Cols > 0 && msg.Rows > 0 {
					setTermSize(ptmx, msg.Cols, msg.Rows)
				}
			case "heartbeat":
				// echo back
				ws.WriteMessage(websocket.TextMessage, p)
			case "cmd":
				ptmx.Write([]byte(msg.Data))
			}
			continue
		}

		// Raw terminal input (plain text from xterm.js)
		ptmx.Write(p)
	}
}

// setTermSize sets the terminal window size on the PTY.
func setTermSize(f *os.File, cols, rows int) {
	pty.Setsize(f, &pty.Winsize{
		Rows: uint16(rows),
		Cols: uint16(cols),
	})
}
