package proxy

import (
	"strconv"
	"strings"
)

const ClientIdentityPrefix = "zc:"

// ClientIdentity is the opaque engine-visible name for a persisted Client.
// Client emails are only unique within an inbound and cannot safely identify
// accounting records returned by Xray or the Clash API.
func ClientIdentity(id uint) string {
	return ClientIdentityPrefix + strconv.FormatUint(uint64(id), 10)
}

// ParseClientIdentity accepts only a non-zero canonical Client identity.
func ParseClientIdentity(value string) (uint, bool) {
	if !strings.HasPrefix(value, ClientIdentityPrefix) {
		return 0, false
	}
	n, err := strconv.ParseUint(strings.TrimPrefix(value, ClientIdentityPrefix), 10, 0)
	if err != nil || n == 0 || uint64(uint(n)) != n || ClientIdentity(uint(n)) != value {
		return 0, false
	}
	return uint(n), true
}
