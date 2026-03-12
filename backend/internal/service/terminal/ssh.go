package terminal

import (
	"encoding/json"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"golang.org/x/crypto/ssh"
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

type sshCredentials struct {
	User string `json:"user"`
	Pass string `json:"pass"`
}

// HandleTerminalWebSocket upgrades the HTTP request to a WebSocket and bridges to SSH.
// The client must send JSON {"user":"...","pass":"..."} as the very first message after connecting.
func HandleTerminalWebSocket(c *gin.Context) {
	ws, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		log.Printf("Failed to upgrade websocket: %v", err)
		return
	}
	defer ws.Close()

	// Wait for SSH credentials as the first message (30-second window)
	ws.SetReadDeadline(time.Now().Add(30 * time.Second))
	_, msg, err := ws.ReadMessage()
	ws.SetReadDeadline(time.Time{}) // reset after auth
	if err != nil {
		ws.WriteMessage(websocket.TextMessage, []byte("\r\n\x1b[31m[Authentication timeout]\x1b[0m\r\n"))
		return
	}

	var creds sshCredentials
	if jsonErr := json.Unmarshal(msg, &creds); jsonErr != nil || creds.User == "" {
		ws.WriteMessage(websocket.TextMessage, []byte("\r\n\x1b[31m[Invalid credentials format]\x1b[0m\r\n"))
		return
	}

	sshConfig := &ssh.ClientConfig{
		User: creds.User,
		Auth: []ssh.AuthMethod{
			ssh.Password(creds.Pass),
		},
		// MITM on loopback 127.0.0.1 is not realistic when panel and sshd run on the same host.
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
	}

	client, err := ssh.Dial("tcp", "127.0.0.1:22", sshConfig)
	if err != nil {
		ws.WriteMessage(websocket.TextMessage, []byte("\r\n\x1b[31mSSH failed: "+err.Error()+"\x1b[0m\r\n"))
		return
	}
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		ws.WriteMessage(websocket.TextMessage, []byte("\r\n\x1b[31mFailed to create SSH session: "+err.Error()+"\x1b[0m\r\n"))
		return
	}
	defer session.Close()

	modes := ssh.TerminalModes{
		ssh.ECHO:          1,
		ssh.TTY_OP_ISPEED: 14400,
		ssh.TTY_OP_OSPEED: 14400,
	}
	if err := session.RequestPty("xterm-256color", 40, 80, modes); err != nil {
		ws.WriteMessage(websocket.TextMessage, []byte("\r\n\x1b[31mPTY request failed: "+err.Error()+"\x1b[0m\r\n"))
		return
	}

	stdin, err := session.StdinPipe()
	if err != nil {
		return
	}
	stdout, err := session.StdoutPipe()
	if err != nil {
		return
	}
	stderr, err := session.StderrPipe()
	if err != nil {
		return
	}

	go func() {
		buf := make([]byte, 1024)
		for {
			n, err := stdout.Read(buf)
			if err != nil {
				return
			}
			ws.WriteMessage(websocket.BinaryMessage, buf[:n])
		}
	}()

	go func() {
		buf := make([]byte, 1024)
		for {
			n, err := stderr.Read(buf)
			if err != nil {
				return
			}
			ws.WriteMessage(websocket.BinaryMessage, buf[:n])
		}
	}()

	if err := session.Shell(); err != nil {
		return
	}

	for {
		messageType, p, err := ws.ReadMessage()
		if err != nil {
			break
		}
		if messageType == websocket.TextMessage || messageType == websocket.BinaryMessage {
			stdin.Write(p)
		}
	}
}
