package config

// Expose models globally so we can AutoMigrate them in the main runner
import (
	"crypto/rand"
	"encoding/base64"
	"log"
	"os"
	"strconv"

	"github.com/glebarez/sqlite"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/model"
	"gorm.io/gorm"
)

var DB *gorm.DB

// InitDB initializes the SQLite database and performs auto-migration
func InitDB(dbPath string) {
	database, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}

	// Pre-migration: add missing columns to existing tables BEFORE AutoMigrate.
	// SQLite's ALTER TABLE can't add NOT NULL columns without defaults, and GORM's
	// AutoMigrate recreates the table — which fails if existing rows have NULL in
	// NOT NULL columns. This step ensures columns exist so AutoMigrate succeeds.
	preMigrateClientColumns(database)

	err = database.AutoMigrate(
		&model.Inbound{},
		&model.Client{},
		&model.RoutingRule{},
		&model.Setting{},
		&model.AdminUser{},
		&model.CronJob{},
	)
	if err != nil {
		log.Fatalf("Failed to auto migrate database: %v", err)
	}
	if err := migrateClientSchema(database); err != nil {
		log.Fatalf("Failed to migrate client schema: %v", err)
	}

	// Audit log migration is non-fatal — don't block startup if it fails
	if err := database.AutoMigrate(&model.AuditLog{}); err != nil {
		log.Printf("Warning: failed to migrate AuditLog table: %v", err)
	}

	// Smart Deploy tables (Phase 1). Non-fatal: an older panel should still
	// boot if these migrations fail; smart deploy simply becomes unavailable.
	if err := database.AutoMigrate(&model.Deployment{}, &model.DeploymentOp{}); err != nil {
		log.Printf("Warning: failed to migrate Smart Deploy tables: %v", err)
	}

	// Outbound table (Phase E). Non-fatal for backwards compat with older DBs.
	if err := database.AutoMigrate(&model.Outbound{}); err != nil {
		log.Printf("Warning: failed to migrate Outbound table: %v", err)
	}

	// Site table (Phase J). Non-fatal for backwards compat with older DBs.
	if err := database.AutoMigrate(&model.Site{}); err != nil {
		log.Printf("Warning: failed to migrate Site table: %v", err)
	}

	DB = database
	log.Println("Database initialized and migrated successfully")
}

// GetSetting retrieves a setting value by key, returns empty string if not found
func GetSetting(key string) string {
	var s model.Setting
	if err := DB.Where("`key` = ?", key).First(&s).Error; err != nil {
		return ""
	}
	return s.Value
}

// SetSetting upserts a setting key-value pair
func SetSetting(key, value string) error {
	var s model.Setting
	result := DB.Where("`key` = ?", key).First(&s)
	if result.Error != nil {
		// Create new
		return DB.Create(&model.Setting{Key: key, Value: value}).Error
	}
	// Update
	s.Value = value
	return DB.Save(&s).Error
}

// EnsureJWTSecret generates and persists a random JWT secret if one doesn't exist
func EnsureJWTSecret() []byte {
	existing := GetSetting("jwt_secret")
	if existing != "" {
		decoded, err := base64.StdEncoding.DecodeString(existing)
		if err == nil && len(decoded) >= 32 {
			return decoded
		}
	}
	// Generate a new 32-byte random secret
	secret := make([]byte, 32)
	_, err := rand.Read(secret)
	if err != nil {
		log.Fatalf("Failed to generate JWT secret: %v", err)
	}
	encoded := base64.StdEncoding.EncodeToString(secret)
	if err := SetSetting("jwt_secret", encoded); err != nil {
		log.Fatalf("Failed to persist JWT secret: %v", err)
	}
	log.Println("Generated and persisted new JWT secret")
	return secret
}

// IsSetupDone checks the DB for setup completion status
func IsSetupDone() bool {
	return GetSetting("setup_complete") == "true"
}

// MarkSetupDone persists setup completion to the DB
func MarkSetupDone() error {
	return SetSetting("setup_complete", "true")
}

// EnsurePort returns the panel's listen port.
// Priority: ZENITH_PORT env var > DB setting > random generation (10000-65535).
// The env var allows Docker users to align the internal port with their -p mapping.
func EnsurePort() string {
	// Highest priority: environment variable override
	if envPort := os.Getenv("ZENITH_PORT"); envPort != "" {
		// Persist to DB so it's consistent across restarts
		if err := SetSetting("port", envPort); err != nil {
			log.Fatalf("Failed to persist port: %v", err)
		}
		return envPort
	}

	existing := GetSetting("port")
	if existing != "" {
		return existing
	}

	// Generate 2 random bytes to derive a port in [10000, 65535]
	b := make([]byte, 2)
	if _, err := rand.Read(b); err != nil {
		log.Fatalf("Failed to generate random port: %v", err)
	}
	n := int(b[0])<<8 | int(b[1])
	port := 10000 + (n % 55536) // range: 10000–65535
	portStr := strconv.Itoa(port)
	if err := SetSetting("port", portStr); err != nil {
		log.Fatalf("Failed to persist port: %v", err)
	}
	log.Printf("Generated random listen port: %s (saved to DB)", portStr)
	return portStr
}
