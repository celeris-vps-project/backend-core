package domain

import "testing"

// stubEvent implements DomainEvent for testing.
type stubEvent struct{ name string }

func (e stubEvent) EventName() string { return e.name }

func TestProduct_RaiseAndCollectEvents(t *testing.T) {
	p, _ := NewProduct("p-evt", "VPS Event", "vps-evt", "US-nyc", 1, 1024, 20, 1000, 499, "USD", BillingMonthly, 10)

	// Initially no events
	if len(p.CollectEvents()) != 0 {
		t.Fatal("expected no events initially")
	}

	// Raise events
	p.RaiseEvent(stubEvent{"test.event1"})
	p.RaiseEvent(stubEvent{"test.event2"})

	events := p.CollectEvents()
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[0].EventName() != "test.event1" {
		t.Fatalf("expected event1, got %s", events[0].EventName())
	}

	// After collecting, events should be cleared
	if len(p.CollectEvents()) != 0 {
		t.Fatal("expected events cleared after collect")
	}
}

func TestProduct_RegionID(t *testing.T) {
	p, _ := NewProduct("p-region", "VPS Region", "vps-region", "DE-fra", 1, 1024, 20, 1000, 499, "USD", BillingMonthly, 10)

	if p.RegionID() != "" {
		t.Fatal("expected empty regionID initially")
	}

	p.SetRegionID("region-123")
	if p.RegionID() != "region-123" {
		t.Fatalf("expected region-123, got %s", p.RegionID())
	}
}
