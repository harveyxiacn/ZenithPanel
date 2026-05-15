// Package apitoken implements ZenithPanel's API-token format used by the
// headless CLI and automation. The plaintext encoding is:
//
//	ztk_<22 base64url chars of 16 random bytes>_<6 hex chars of CRC32>
//
// Only sha256(plaintext) is stored at rest; on lookup the panel hashes the
// presented secret and matches it against ApiToken rows with subtle compare.
package apitoken

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"hash/crc32"
	"strings"
)

// Prefix marks a token as belonging to ZenithPanel. Used by the auth
// middleware to route Bearer tokens to the right validator path.
const Prefix = "ztk_"

// Generate returns the (plaintext, sha256-hex) pair for a new token.
// The caller must show the plaintext to the user exactly once and persist
// only the hash.
func Generate() (plaintext, hashHex string, err error) {
	raw := make([]byte, 16)
	if _, err = rand.Read(raw); err != nil {
		return "", "", err
	}
	body := base64.RawURLEncoding.EncodeToString(raw)
	checksum := fmt.Sprintf("%06x", crc32.ChecksumIEEE(raw)&0xFFFFFF)
	plaintext = Prefix + body + "_" + checksum
	return plaintext, Hash(plaintext), nil
}

// Hash returns the hex sha256 of the token. The hex form is stable across
// platforms and easy to inspect during debugging.
func Hash(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}

// IsWellFormed reports whether s has the structural shape of a ZenithPanel
// API token. Cheap pre-check before hitting the DB.
//
// The body uses base64 raw-url-encoding which means it can legitimately
// contain `_` characters; we therefore split on the LAST underscore rather
// than the first. An earlier version used `strings.Split(rest, "_")` which
// rejected ~1 in 22 generated tokens at random.
func IsWellFormed(s string) bool {
	if !strings.HasPrefix(s, Prefix) {
		return false
	}
	rest := s[len(Prefix):]
	idx := strings.LastIndex(rest, "_")
	if idx < 0 {
		return false
	}
	body, sum := rest[:idx], rest[idx+1:]
	if len(body) != 22 || len(sum) != 6 {
		return false
	}
	raw, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil || len(raw) != 16 {
		return false
	}
	expect := fmt.Sprintf("%06x", crc32.ChecksumIEEE(raw)&0xFFFFFF)
	return expect == sum
}

// ErrMalformed is returned when a token fails the structural check.
var ErrMalformed = errors.New("malformed api token")
