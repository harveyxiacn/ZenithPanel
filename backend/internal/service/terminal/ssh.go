package terminal

import (
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"golang.org/x/crypto/ssh"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true // Allow all for now, in prod restrict to panel domain
	},
}

// HandleTerminalWebSocket upgrades the HTTP request to a WebSocket and bridges to SSH
func HandleTerminalWebSocket(c *gin.Context) {
	ws, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		log.Printf("Failed to upgrade websocket: %v", err)
		return
	}
	defer ws.Close()

	// Parse parameters (host, user, password from query or context)
	// For local ZenithPanel, we usually connect to 127.0.0.1 with local credentials or keys
	// Dummy connection block to illustrate the concept securely
	user := c.Query("user")
	pass := c.Query("pass")
	if user == "" {
		user = "root"
	}

	config := &ssh.ClientConfig{
		User: user,
		Auth: []ssh.AuthMethod{
			ssh.Password(pass),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}

	client, err := ssh.Dial("tcp", "127.0.0.1:22", config)
	if err != nil {
		ws.WriteMessage(websocket.TextMessage, []byte("\r\nFailed to connect to local SSH daemon: "+err.Error()+"\r\n"))
		return
	}
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		ws.WriteMessage(websocket.TextMessage, []byte("\r\nFailed to create SSH session: "+err.Error()+"\r\n"))
		return
	}
	defer session.Close()

	// Set up terminal modes
	modes := ssh.TerminalModes{
		ssh.ECHO:          1,     // enable echoing
		ssh.TTY_OP_ISPEED: 14400, // input speed = 14.4kbaud
		ssh.TTY_OP_OSPEED: 14400, // output speed = 14.4kbaud
	}

	// Request pseudo terminal
	if err := session.RequestPty("xterm-256color", 40, 80, modes); err != nil {
		ws.WriteMessage(websocket.TextMessage, []byte("\r\nRequest for pseudo terminal failed: "+err.Error()+"\r\n"))
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
			ws.WriteMessage(websocket.TextMessage, buf[:n])
		}
	}()

	go func() {
		buf := make([]byte, 1024)
		for {
			n, err := stderr.Read(buf)
			if err != nil {
				return
			}
			ws.WriteMessage(websocket.TextMessage, buf[:n])
		}
	}()

	// Start remote shell
	if err := session.Shell(); err != nil {
		return
	}

	// Read from WebSocket and write to SSH stdin
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
