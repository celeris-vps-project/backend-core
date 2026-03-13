package domain

import (
	"testing"
	"time"
)

func TestBillingCycle_Validation(t *testing.T) {
	validCycles := []string{BillingCycleOneTime, BillingCycleMonthly, BillingCycleYearly}
	for _, ct := range validCycles {
		bc, err := NewBillingCycle(ct)
		if err != nil {
			t.Fatalf("expected no error for %q, got %v", ct, err)
		}
		if bc.Type() != ct {
			t.Fatalf("expected type %q, got %q", ct, bc.Type())
		}
	}

	if _, err := NewBillingCycle("weekly"); err == nil {
		t.Fatal("expected error for invalid cycle type")
	}
}

func TestBillingCycle_IsRecurring(t *testing.T) {
	oneTime := OneTimeCycle()
	if oneTime.IsRecurring() {
		t.Fatal("one_time should not be recurring")
	}

	monthly, _ := NewBillingCycle(BillingCycleMonthly)
	if !monthly.IsRecurring() {
		t.Fatal("monthly should be recurring")
	}

	yearly, _ := NewBillingCycle(BillingCycleYearly)
	if !yearly.IsRecurring() {
		t.Fatal("yearly should be recurring")
	}
}

func TestBillingCycle_MonthlyNextPeriod(t *testing.T) {
	monthly, _ := NewBillingCycle(BillingCycleMonthly)

	tests := []struct {
		name      string
		from      time.Time
		wantStart time.Time
		wantEnd   time.Time
	}{
		{
			name:      "normal day",
			from:      time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC),
			wantStart: time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC),
			wantEnd:   time.Date(2026, 4, 15, 0, 0, 0, 0, time.UTC),
		},
		{
			name:      "Jan 31 → Feb 28 (non-leap)",
			from:      time.Date(2025, 1, 31, 0, 0, 0, 0, time.UTC),
			wantStart: time.Date(2025, 1, 31, 0, 0, 0, 0, time.UTC),
			wantEnd:   time.Date(2025, 2, 28, 0, 0, 0, 0, time.UTC),
		},
		{
			name:      "Jan 31 → Feb 29 (leap year)",
			from:      time.Date(2024, 1, 31, 0, 0, 0, 0, time.UTC),
			wantStart: time.Date(2024, 1, 31, 0, 0, 0, 0, time.UTC),
			wantEnd:   time.Date(2024, 2, 29, 0, 0, 0, 0, time.UTC),
		},
		{
			name:      "Mar 31 → Apr 30",
			from:      time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC),
			wantStart: time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC),
			wantEnd:   time.Date(2026, 4, 30, 0, 0, 0, 0, time.UTC),
		},
		{
			name:      "Dec 31 → Jan 31 (year rollover)",
			from:      time.Date(2025, 12, 31, 0, 0, 0, 0, time.UTC),
			wantStart: time.Date(2025, 12, 31, 0, 0, 0, 0, time.UTC),
			wantEnd:   time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start, end := monthly.NextPeriod(tt.from)
			if !start.Equal(tt.wantStart) {
				t.Errorf("start: got %v, want %v", start, tt.wantStart)
			}
			if !end.Equal(tt.wantEnd) {
				t.Errorf("end: got %v, want %v", end, tt.wantEnd)
			}
		})
	}
}

func TestBillingCycle_YearlyNextPeriod(t *testing.T) {
	yearly, _ := NewBillingCycle(BillingCycleYearly)

	tests := []struct {
		name      string
		from      time.Time
		wantStart time.Time
		wantEnd   time.Time
	}{
		{
			name:      "normal day",
			from:      time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC),
			wantStart: time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC),
			wantEnd:   time.Date(2027, 6, 15, 0, 0, 0, 0, time.UTC),
		},
		{
			name:      "Feb 29 leap → Feb 28 non-leap",
			from:      time.Date(2024, 2, 29, 0, 0, 0, 0, time.UTC),
			wantStart: time.Date(2024, 2, 29, 0, 0, 0, 0, time.UTC),
			wantEnd:   time.Date(2025, 2, 28, 0, 0, 0, 0, time.UTC),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start, end := yearly.NextPeriod(tt.from)
			if !start.Equal(tt.wantStart) {
				t.Errorf("start: got %v, want %v", start, tt.wantStart)
			}
			if !end.Equal(tt.wantEnd) {
				t.Errorf("end: got %v, want %v", end, tt.wantEnd)
			}
		})
	}
}

func TestBillingCycle_OneTimeNextPeriod(t *testing.T) {
	oneTime := OneTimeCycle()
	from := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)
	start, end := oneTime.NextPeriod(from)
	if !start.Equal(from) || !end.Equal(from) {
		t.Errorf("one_time: start and end should both equal from, got start=%v end=%v", start, end)
	}
}
