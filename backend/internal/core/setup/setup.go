package setup

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"log"

	"github.com/harveyxiacn/ZenithPanel/backend/internal/config"
)

var (
	pendingToken  string
	pendingSuffix string
)

func generateRandomString(n int) string {
	b := make([]byte, n)
	_, err := rand.Read(b)
	if err != nil {
		log.Fatalf("Error generating random string: %v", err)
	}
	return base64.URLEncoding.EncodeToString(b)[:n]
}

// PrintSetupInstructions outputs the setup credentials to the terminal.
// It is called once at startup and again after the port is resolved.
func PrintSetupInstructions(token, suffix, port string) {
	fmt.Println("=========================================================")
	fmt.Println("             ZenithPanel Setup Wizard")
	fmt.Println("=========================================================")
	fmt.Println("First launch detected. One-time security credentials generated.")
	fmt.Println("Open the following URL in your browser to complete setup:")
	fmt.Printf("\n  URL:      http://<YOUR_IP>:%s/zenith-setup-%s\n", port, suffix)
	fmt.Printf("  Password: %s\n\n", token)
	fmt.Println("This URL and password expire after setup is completed.")
	fmt.Println("=========================================================")
}

// InitSetup checks the persistent DB state for setup completion.
// If not complete, generates secure console-only one-time credentials.
func InitSetup() {
	cfg := config.GetConfig()

	// Check persistent state from DB
	if config.IsSetupDone() {
		cfg.IsSetupComplete = true
		log.Println("Setup already completed, skipping wizard.")
		return
	}

	// Generate one-time secure credentials
	setupSuffix := generateRandomString(8)
	oneTimePassword := generateRandomString(16)

	cfg.SetupURLSuffix = setupSuffix
	cfg.SetupOneTimeToken = oneTimePassword
	cfg.IsSetupComplete = false

	pendingToken = oneTimePassword
	pendingSuffix = setupSuffix
}

// PrintSetupIfPending prints the setup instructions once the listen port is known.
func PrintSetupIfPending(port string) {
	if pendingToken != "" {
		PrintSetupInstructions(pendingToken, pendingSuffix, port)
	}
}
