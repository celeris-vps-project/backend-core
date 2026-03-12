package domain

import "testing"

func TestNewRegion_Valid(t *testing.T) {
	r, err := NewRegion("r-1", "DE-fra", "Frankfurt, Germany", "--------")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.ID() != "r-1" {
		t.Fatalf("expected id r-1, got %s", r.ID())
	}
	if r.Code() != "DE-fra" {
		t.Fatalf("expected code DE-fra, got %s", r.Code())
	}
	if r.Name() != "Frankfurt, Germany" {
		t.Fatalf("expected name Frankfurt, Germany, got %s", r.Name())
	}
	if r.FlagIcon() != "--------" {
		t.Fatalf("expected flag icon --------, got %s", r.FlagIcon())
	}
	if r.Status() != RegionStatusActive {
		t.Fatalf("expected status active, got %s", r.Status())
	}
	if !r.IsActive() {
		t.Fatal("expected region to be active")
	}
}

func TestNewRegion_RequiredFields(t *testing.T) {
	tests := []struct {
		name            string
		id, code, rname string
	}{
		{"missing id", "", "DE-fra", "Frankfurt"},
		{"missing code", "r-1", "", "Frankfurt"},
		{"missing name", "r-1", "DE-fra", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewRegion(tc.id, tc.code, tc.rname, "")
			if err == nil {
				t.Fatal("expected error for missing required field")
			}
		})
	}
}

func TestRegion_ActivateDeactivate(t *testing.T) {
	r, _ := NewRegion("r-1", "US-slc", "Salt Lake City, USA", "--------")

	r.Deactivate()
	if r.Status() != RegionStatusInactive {
		t.Fatalf("expected inactive, got %s", r.Status())
	}
	if r.IsActive() {
		t.Fatal("expected region to be inactive")
	}

	r.Activate()
	if r.Status() != RegionStatusActive {
		t.Fatalf("expected active, got %s", r.Status())
	}
	if !r.IsActive() {
		t.Fatal("expected region to be active")
	}
}

func TestReconstituteRegion(t *testing.T) {
	r := ReconstituteRegion("r-1", "JP-tky", "Tokyo, Japan", "--------", RegionStatusInactive)
	if r.ID() != "r-1" {
		t.Fatalf("expected id r-1, got %s", r.ID())
	}
	if r.Status() != RegionStatusInactive {
		t.Fatalf("expected inactive, got %s", r.Status())
	}
}
