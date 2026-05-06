package domain

import "time"

type TrafficUsageRecord struct {
	ID         uint64
	InstanceID string
	RX         uint64
	TX         uint64
	CreateAt   time.Time
}

type TrafficCursor struct {
	ID         uint64
	InstanceID string
	LastRX     uint64
	LastTX     uint64
	CreateAt   time.Time
}

type TrafficDaily struct {
	ID         uint64
	InstanceID string
	Date       time.Time
	RX         uint64
	TX         uint64
	CreateAt   time.Time
}

type TrafficBillingState struct {
	InstanceID      string
	LastEndPeriodAt time.Time
	CreateAt        time.Time
	UpdatedAt       time.Time
}

type TrafficUsageSummary struct {
	InstanceID      string
	LastEndPeriodAt time.Time
	PeriodStart     time.Time
	PeriodEnd       time.Time
	RX              uint64
	TX              uint64
	Total           uint64
	BandwidthGB     int
	PeriodMax       uint64
	OverLimit       bool
	Daily           []TrafficDaily
}
