package infra

import "github.com/google/uuid"

// UUIDGenerator produces unique invoice IDs.
type UUIDGenerator struct{}

func NewUUIDGenerator() *UUIDGenerator {
	return &UUIDGenerator{}
}

func (g *UUIDGenerator) NewID() string {
	return uuid.New().String()
}
