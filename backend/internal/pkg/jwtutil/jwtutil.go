package jwtutil

import (
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// SecretKey is set at startup from the persistent DB value
var SecretKey []byte

// InitSecret must be called once at startup with the key from config.EnsureJWTSecret()
func InitSecret(key []byte) {
	SecretKey = key
}

type Claims struct {
	UserID   string `json:"user_id"`
	Username string `json:"username"`
	jwt.RegisteredClaims
}

// GenerateToken creates a JWT for a user
func GenerateToken(userID, username string, duration time.Duration) (string, error) {
	if len(SecretKey) == 0 {
		return "", errors.New("JWT secret not initialized")
	}
	claims := Claims{
		UserID:   userID,
		Username: username,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(duration)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(SecretKey)
}

// ValidateToken parses and validates a JWT
func ValidateToken(tokenString string) (*Claims, error) {
	if len(SecretKey) == 0 {
		return nil, errors.New("JWT secret not initialized")
	}
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(token *jwt.Token) (any, error) {
		return SecretKey, nil
	})

	if err != nil {
		return nil, err
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, errors.New("invalid token")
	}

	return claims, nil
}
