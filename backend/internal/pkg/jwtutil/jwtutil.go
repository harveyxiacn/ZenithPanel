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
	UserID         string `json:"user_id"`
	Username       string `json:"username"`
	Purpose        string `json:"purpose,omitempty"`
	SessionVersion uint64 `json:"session_version,omitempty"`
	LoggedInAt     int64  `json:"logged_in_at,omitempty"`
	jwt.RegisteredClaims
}

const (
	PurposeAdmin = "admin"
	PurposeSetup = "setup"
)

// MaxSessionAge caps a continuously refreshed browser session.
const MaxSessionAge = 7 * 24 * time.Hour

// GenerateToken creates a JWT for a user
func GenerateToken(userID, username string, duration time.Duration) (string, error) {
	return GenerateSessionToken(userID, username, 0, time.Now(), duration)
}

// GenerateSessionToken creates an administrator JWT bound to its database
// session version and original successful-login time.
func GenerateSessionToken(userID, username string, sessionVersion uint64, loggedInAt time.Time, duration time.Duration) (string, error) {
	if loggedInAt.IsZero() {
		return "", errors.New("session login time is required")
	}
	return generateToken(userID, username, PurposeAdmin, sessionVersion, loggedInAt, duration)
}

// GenerateSetupToken creates a purpose-bound JWT accepted only by the setup
// completion middleware. It can never authenticate to the post-setup API.
func GenerateSetupToken(duration time.Duration) (string, error) {
	return generateToken("setup-admin", "setup", PurposeSetup, 0, time.Time{}, duration)
}

func generateToken(userID, username, purpose string, sessionVersion uint64, loggedInAt time.Time, duration time.Duration) (string, error) {
	if len(SecretKey) == 0 {
		return "", errors.New("JWT secret not initialized")
	}
	expires := time.Now().Add(duration)
	if purpose == PurposeAdmin && loggedInAt.Add(MaxSessionAge).Before(expires) {
		expires = loggedInAt.Add(MaxSessionAge)
	}
	claims := Claims{
		UserID:         userID,
		Username:       username,
		Purpose:        purpose,
		SessionVersion: sessionVersion,
		LoggedInAt:     loggedInAt.Unix(),
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(expires),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			Issuer:    "zenithpanel",
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
	},
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer("zenithpanel"),
		jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(),
	)

	if err != nil {
		return nil, err
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, errors.New("invalid token")
	}
	if claims.Purpose == PurposeAdmin {
		login := time.Unix(claims.LoggedInAt, 0)
		if claims.LoggedInAt <= 0 || login.After(time.Now().Add(time.Second)) || !time.Now().Before(login.Add(MaxSessionAge)) {
			return nil, errors.New("session absolute lifetime exceeded or invalid login time")
		}
	}

	return claims, nil
}
