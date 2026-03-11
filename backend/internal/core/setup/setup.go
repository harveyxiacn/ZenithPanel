package setup

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"log"

	"github.com/harveyxiacn/ZenithPanel/backend/internal/config"
)

func generateRandomString(n int) string {
	b := make([]byte, n)
	_, err := rand.Read(b)
	if err != nil {
		log.Fatalf("Error generating random string: %v", err)
	}
	return base64.URLEncoding.EncodeToString(b)[:n]
}

// PrintSetupInstructions outputs the setup credentials to the terminal
func PrintSetupInstructions(token, suffix string) {
	fmt.Println("=========================================================")
	fmt.Println("             🔒 ZenithPanel 安全初始化向导 🔒              ")
	fmt.Println("=========================================================")
	fmt.Println("检测到系统为首次启动，为了您的安全，已为您生成一次性安全凭证。")
	fmt.Println("请使用以下信息在浏览器登录并完成设置：")
	fmt.Printf("\n  访问地址: http://<你的IP>:8080/zenith-setup-%s\n", suffix)
	fmt.Printf("  初始密码: %s\n\n", token)
	fmt.Println("提示: 成功完成安装向导后，此地址与密码将永久失效！")
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

	PrintSetupInstructions(oneTimePassword, setupSuffix)
}

