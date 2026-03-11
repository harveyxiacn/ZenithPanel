package jwtutil

import (
	"crypto/rand"
	"testing"
	"time"
)

func TestInitSecret(t *testing.T) {
	secret := make([]byte, 32)
	rand.Read(secret)
	InitSecret(secret)
	if len(SecretKey) != 32 {
		t.Fatalf("Expected SecretKey length to be 32, got %d", len(SecretKey))
	}
}

func TestGenerateAndValidateToken(t *testing.T) {
	// Initialize Secret
	secret := make([]byte, 32)
	rand.Read(secret)
	InitSecret(secret)

	// Generate
	token, err := GenerateToken("user123", "admin", time.Hour)
	if err != nil {
		t.Fatalf("Failed to generate token: %v", err)
	}
	if token == "" {
		t.Fatalf("Generated token is empty")
	}

	// Validate valid token
	claims, err := ValidateToken(token)
	if err != nil {
		t.Fatalf("Failed to validate token: %v", err)
	}
	if claims.UserID != "user123" {
		t.Errorf("Expected user ID 'user123', got '%s'", claims.UserID)
	}
	if claims.Username != "admin" {
		t.Errorf("Expected username 'admin', got '%s'", claims.Username)
	}

	// Validate tampered token
	tampered := token + "123"
	_, err = ValidateToken(tampered)
	if err == nil {
		t.Fatalf("Expected error for tampered token, got nil")
	}
}

func TestUninitializedSecret(t *testing.T) {
	// Clear secret
	InitSecret(nil)

	_, err := GenerateToken("user123", "admin", time.Hour)
	if err == nil || err.Error() != "JWT secret not initialized" {
		t.Fatalf("Expected error 'JWT secret not initialized', got %v", err)
	}

	_, err = ValidateToken("some.fake.token")
	if err == nil || err.Error() != "JWT secret not initialized" {
		t.Fatalf("Expected error 'JWT secret not initialized', got %v", err)
	}
}
