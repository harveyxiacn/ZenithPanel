package terminal

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"sync"
	"time"
)

const ticketTTL = 30 * time.Second
const maxPendingTickets = 1024

type ticketRecord struct {
	principal string
	ip        string
	expiresAt time.Time
	issuedAt  time.Time
}

var ticketStore = struct {
	sync.Mutex
	tickets map[[32]byte]ticketRecord
}{tickets: make(map[[32]byte]ticketRecord)}

// IssueTicket creates a short-lived, single-use WebSocket credential. Only a
// SHA-256 digest is retained server-side so a memory dump cannot reveal live
// ticket values.
func IssueTicket(principal, ip string) (string, time.Time, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", time.Time{}, fmt.Errorf("generate terminal ticket: %w", err)
	}
	value := base64.RawURLEncoding.EncodeToString(raw)
	hash := sha256.Sum256([]byte(value))
	now := time.Now()
	expires := now.Add(ticketTTL)

	ticketStore.Lock()
	defer ticketStore.Unlock()
	for key, record := range ticketStore.tickets {
		if now.After(record.expiresAt) {
			delete(ticketStore.tickets, key)
		}
	}
	if len(ticketStore.tickets) >= maxPendingTickets {
		var oldestKey [32]byte
		var oldest time.Time
		for key, record := range ticketStore.tickets {
			if oldest.IsZero() || record.issuedAt.Before(oldest) {
				oldestKey, oldest = key, record.issuedAt
			}
		}
		delete(ticketStore.tickets, oldestKey)
	}
	ticketStore.tickets[hash] = ticketRecord{principal: principal, ip: ip, expiresAt: expires, issuedAt: now}
	return value, expires, nil
}

// ConsumeTicket validates and deletes a ticket atomically. A failed IP check
// also consumes it, preventing repeated guesses or reuse from another host.
func ConsumeTicket(value, ip string) (string, bool) {
	if value == "" {
		return "", false
	}
	hash := sha256.Sum256([]byte(value))
	ticketStore.Lock()
	record, ok := ticketStore.tickets[hash]
	if ok {
		delete(ticketStore.tickets, hash)
	}
	ticketStore.Unlock()
	if !ok || time.Now().After(record.expiresAt) || record.ip != ip {
		return "", false
	}
	return record.principal, true
}
