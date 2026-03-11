package perf

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"
)

const (
	perfTicketTTL       = 30 * time.Second
	perfCleanupInterval = 60 * time.Second
)

type perfTicketEntry struct {
	UserID    string
	Role      string
	ExpiresAt time.Time
}

// PerfTicketStore is a concurrency-safe store for short-lived WS tickets.
type PerfTicketStore struct {
	mu      sync.Mutex
	tickets map[string]perfTicketEntry
}

// NewPerfTicketStore creates a ticket store with background cleanup.
func NewPerfTicketStore() *PerfTicketStore {
	s := &PerfTicketStore{tickets: make(map[string]perfTicketEntry)}
	go s.cleanup()
	return s
}

// Issue generates a random ticket.
func (s *PerfTicketStore) Issue(userID, role string) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate ticket: %w", err)
	}
	ticket := hex.EncodeToString(b)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tickets[ticket] = perfTicketEntry{
		UserID:    userID,
		Role:      role,
		ExpiresAt: time.Now().Add(perfTicketTTL),
	}
	return ticket, nil
}

// Redeem validates and consumes a ticket (one-time use).
func (s *PerfTicketStore) Redeem(ticket string) (userID, role string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.tickets[ticket]
	if !ok {
		return "", "", fmt.Errorf("unknown or already used ticket")
	}
	delete(s.tickets, ticket)
	if time.Now().After(entry.ExpiresAt) {
		return "", "", fmt.Errorf("ticket expired")
	}
	return entry.UserID, entry.Role, nil
}

func (s *PerfTicketStore) cleanup() {
	ticker := time.NewTicker(perfCleanupInterval)
	defer ticker.Stop()
	for range ticker.C {
		now := time.Now()
		s.mu.Lock()
		for k, v := range s.tickets {
			if now.After(v.ExpiresAt) {
				delete(s.tickets, k)
			}
		}
		s.mu.Unlock()
	}
}
