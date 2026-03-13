package domain

import (
	"errors"
	"time"
)

const (
	BillingCycleOneTime = "one_time"
	BillingCycleMonthly = "monthly"
	BillingCycleYearly  = "yearly"
)

// BillingCycle is a value object representing the billing cadence for an invoice.
type BillingCycle struct {
	cycleType string
}

// NewBillingCycle validates and returns a BillingCycle.
func NewBillingCycle(cycleType string) (BillingCycle, error) {
	switch cycleType {
	case BillingCycleOneTime, BillingCycleMonthly, BillingCycleYearly:
		return BillingCycle{cycleType: cycleType}, nil
	default:
		return BillingCycle{}, errors.New("domain_error: invalid billing cycle, must be one_time, monthly, or yearly")
	}
}

// OneTimeCycle returns a one-time (non-recurring) billing cycle.
func OneTimeCycle() BillingCycle {
	return BillingCycle{cycleType: BillingCycleOneTime}
}

func (bc BillingCycle) Type() string      { return bc.cycleType }
func (bc BillingCycle) IsRecurring() bool  { return bc.cycleType != BillingCycleOneTime }
func (bc BillingCycle) IsZero() bool       { return bc.cycleType == "" }

// NextPeriod computes the next billing period [start, end) based on the given
// reference time. For monthly cycles, the end date is aligned to the month's
// last day to avoid the Go AddDate(0,1,0) drift on dates like Jan-31 → Mar-03.
// For yearly cycles, the same end-of-month alignment is applied for Feb-29.
func (bc BillingCycle) NextPeriod(from time.Time) (start, end time.Time) {
	start = from
	switch bc.cycleType {
	case BillingCycleMonthly:
		end = addMonths(from, 1)
	case BillingCycleYearly:
		end = addYears(from, 1)
	default:
		// one_time — period start == end (no recurrence)
		end = from
	}
	return start, end
}

// addMonths adds n calendar months to t, clamping the day to the last day of
// the target month. This prevents Go's time.AddDate drift:
//
//	Jan-31 + 1 month → Feb-28 (or 29 in leap year), NOT Mar-03.
//	Mar-31 + 1 month → Apr-30, NOT May-01.
func addMonths(t time.Time, n int) time.Time {
	y, m, d := t.Date()
	loc := t.Location()

	// Move to the target month
	targetMonth := time.Month(int(m) + n)
	targetYear := y
	for targetMonth > 12 {
		targetMonth -= 12
		targetYear++
	}
	for targetMonth < 1 {
		targetMonth += 12
		targetYear--
	}

	// Clamp day to the last day of the target month
	lastDay := daysInMonth(targetYear, targetMonth)
	if d > lastDay {
		d = lastDay
	}

	return time.Date(targetYear, targetMonth, d,
		t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), loc)
}

// addYears adds n calendar years to t, clamping the day for leap-year edge
// cases (e.g. Feb-29 + 1 year → Feb-28).
func addYears(t time.Time, n int) time.Time {
	y, m, d := t.Date()
	loc := t.Location()

	targetYear := y + n
	lastDay := daysInMonth(targetYear, m)
	if d > lastDay {
		d = lastDay
	}

	return time.Date(targetYear, m, d,
		t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), loc)
}

// daysInMonth returns the number of days in the given month of the given year.
func daysInMonth(year int, month time.Month) int {
	// The 0th day of the next month is the last day of the current month.
	return time.Date(year, month+1, 0, 0, 0, 0, 0, time.UTC).Day()
}
