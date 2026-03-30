package ws

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"
)

const (
	ticketTTL       = 30 * time.Second
	cleanupInterval = 60 * time.Second
)

type ticketEntry struct {
	UserID    string
	Role      string
	ExpiresAt time.Time
}

// TicketStore stores one-time WebSocket tickets for authenticated users.
type TicketStore struct {
	mu      sync.Mutex
	tickets map[string]ticketEntry
}

func NewTicketStore() *TicketStore {
	s := &TicketStore{tickets: make(map[string]ticketEntry)}
	go s.cleanup()
	return s
}

func (s *TicketStore) Issue(userID, role string) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate ticket: %w", err)
	}
	ticket := hex.EncodeToString(b)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.tickets[ticket] = ticketEntry{
		UserID:    userID,
		Role:      role,
		ExpiresAt: time.Now().Add(ticketTTL),
	}
	return ticket, nil
}

func (s *TicketStore) Redeem(ticket string) (userID, role string, err error) {
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

func (s *TicketStore) cleanup() {
	ticker := time.NewTicker(cleanupInterval)
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
