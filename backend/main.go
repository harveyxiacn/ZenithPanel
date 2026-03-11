package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/api"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/config"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/core/setup"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/docker"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/pkg/jwtutil"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/service/proxy"
)

func main() {
	// Initialize the application
	log.Println("ZenithPanel server starting...")
	
	// 1. Initialize Database
	config.InitDB("zenith.db")

	// 2. Initialize JWT Secret from persistent storage
	secret := config.EnsureJWTSecret()
	jwtutil.InitSecret(secret)

	// 3. Execute Setup Initialization (check persistent state)
	setup.InitSetup()

	// 4. Initialize Managers
	dm, err := docker.NewManager()
	if err != nil {
		log.Printf("Warning: Docker manager init failed: %v", err)
	}
	xm := proxy.NewXrayManager()
	sm := proxy.NewSingboxManager()

	// 5. Create a new Gin router
	r := gin.Default()
	
	// 6. Setup API routes
	api.SetupRoutes(r, dm, xm, sm)
	
	// 6. Define HTTP Server
	srv := &http.Server{
		Addr:    ":8080",
		Handler: r,
	}

	// 7. Run the server in a goroutine so it doesn't block
	go func() {
		log.Println("Server running on port 8080")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Failed to start server: %v", err)
		}
	}()

	// 8. Graceful Shutdown: Wait for interrupt signal
	quit := make(chan os.Signal, 1)
	// kill (no param) default send syscall.SIGTERM
	// kill -2 is syscall.SIGINT
	// kill -9 is syscall.SIGKILL but can't be caught, so don't need to add it
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("Shutting down server...")

	// The context is used to inform the server it has 5 seconds to finish
	// the request it is currently handling
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Fatal("Server forced to shutdown:", err)
	}

	log.Println("Server exiting")
}

