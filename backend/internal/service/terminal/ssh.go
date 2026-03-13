package terminal

import (
	"encoding/json"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"

	"github.com/creack/pty"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	// Only allow WebSocket connections from the same host (prevents Cross-Site WebSocket Hijacking)
	CheckOrigin: func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		if origin == "" {
			return true // non-browser clients
		}
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		return strings.Contains(origin, host)
	},
}

// wsMsg is the JSON protocol for terminal WebSocket messages.
type wsMsg struct {
	Type string `json:"type"` // "cmd", "resize", "heartbeat"
	Data string `json:"data"`
	Cols int    `json:"cols"`
	Rows int    `json:"rows"`
}

// findShell returns the first available shell binary.
func findShell() string {
	for _, sh := range []string{"/bin/bash", "/bin/sh"} {
		if _, err := os.Stat(sh); err == nil {
			return sh
		}
	}
	return "/bin/sh"
}

// HandleTerminalWebSocket upgrades the HTTP request to a WebSocket and spawns
// a local shell via PTY — no SSH credentials required.
func HandleTerminalWebSocket(c *gin.Context) {
	ws, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		log.Printf("Failed to upgrade websocket: %v", err)
		return
	}
	defer ws.Close()

	shell := findShell()
	cmd := exec.Command(shell)
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	if home, err := os.UserHomeDir(); err == nil {
		cmd.Dir = home
	}

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
