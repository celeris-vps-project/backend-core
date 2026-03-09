package domain

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"
)

// BootstrapToken is a one-time-use token that an agent presents during initial
// registration. After successful use it is marked as consumed and cannot be
// reused.
type BootstrapToken struct {
	id           string
	nodeID       string    // the host node this token is issued for
	token        string    // opaque random hex string
	expiresAt    time.Time // token is invalid after this time
	used         bool
	usedByNodeID string // node that consumed this token (same as nodeID on success)
	usedAt       *time.Time
	createdAt    time.Time
	description  string // optional admin note, e.g. "for node DE-fra-03"
}

// NewBootstrapToken creates a new bootstrap token bound to a specific node with the given TTL.
func NewBootstrapToken(id, nodeID string, ttl time.Duration, description string) (*BootstrapToken, error) {
	if id == "" {
		return nil, errors.New("domain_error: id is required")
	}
	if nodeID == "" {
		return nil, errors.New("domain_error: nodeID is required")
	}
	token, err := generateRandomToken(32) // 256-bit
	if err != nil {
		return nil, err
	}
	now := time.Now()
	return &BootstrapToken{
		id:          id,
		nodeID:      nodeID,
		token:       token,
		expiresAt:   now.Add(ttl),
		createdAt:   now,
		description: description,
	}, nil
}

// ReconstituteBootstrapToken rebuilds a BootstrapToken from persistence.
func ReconstituteBootstrapToken(id, nodeID, token string, expiresAt time.Time, used bool, usedByNodeID string, usedAt *time.Time, createdAt time.Time, description string) *BootstrapToken {
	return &BootstrapToken{
		id: id, nodeID: nodeID, token: token, expiresAt: expiresAt,
		used: used, usedByNodeID: usedByNodeID, usedAt: usedAt,
		createdAt: createdAt, description: description,
	}
}

// ---- Accessors ----

func (t *BootstrapToken) ID() string           { return t.id }
func (t *BootstrapToken) NodeID() string       { return t.nodeID }
func (t *BootstrapToken) Token() string        { return t.token }
func (t *BootstrapToken) ExpiresAt() time.Time { return t.expiresAt }
func (t *BootstrapToken) Used() bool           { return t.used }
func (t *BootstrapToken) UsedByNodeID() string { return t.usedByNodeID }
func (t *BootstrapToken) UsedAt() *time.Time   { return t.usedAt }
func (t *BootstrapToken) CreatedAt() time.Time { return t.createdAt }
func (t *BootstrapToken) Description() string  { return t.description }

// IsValid returns true if the token has not been used and has not expired.
func (t *BootstrapToken) IsValid() bool {
	return !t.used && time.Now().Before(t.expiresAt)
}

// Consume marks the token as used. The consuming node is always the one the
// token was originally bound to (t.nodeID) — callers cannot override this.
func (t *BootstrapToken) Consume() error {
	if t.used {
		return errors.New("domain_error: bootstrap token already used")
	}
	if time.Now().After(t.expiresAt) {
		return errors.New("domain_error: bootstrap token expired")
	}
	t.used = true
	t.usedByNodeID = t.nodeID
	now := time.Now()
	t.usedAt = &now
	return nil
}

// ---- Helpers ----

func generateRandomToken(bytes int) (string, error) {
	b := make([]byte, bytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// GenerateNodeToken creates a random opaque token suitable for permanent
// node authentication (256-bit hex string).
func GenerateNodeToken() (string, error) {
	return generateRandomToken(32)
}
