package domain

import "errors"

// Money represents a non-negative amount in minor units (e.g., cents).
type Money struct {
	currency string
	amount   int64
}

func NewMoney(currency string, amount int64) (Money, error) {
	if currency == "" {
		return Money{}, errors.New("domain_error: currency is required")
	}
	if amount < 0 {
		return Money{}, errors.New("domain_error: amount must be >= 0")
	}
	return Money{currency: currency, amount: amount}, nil
}

func ZeroMoney(currency string) Money {
	return Money{currency: currency, amount: 0}
}

func (m Money) Currency() string { return m.currency }
func (m Money) Amount() int64    { return m.amount }
func (m Money) IsZero() bool     { return m.amount == 0 }

func (m Money) Add(other Money) (Money, error) {
	if m.currency != other.currency {
		return Money{}, errors.New("domain_error: currency mismatch")
	}
	return Money{currency: m.currency, amount: m.amount + other.amount}, nil
}

func (m Money) Mul(factor int64) (Money, error) {
	if factor < 0 {
		return Money{}, errors.New("domain_error: factor must be >= 0")
	}
	return Money{currency: m.currency, amount: m.amount * factor}, nil
}

func (m Money) GreaterThanOrEqual(other Money) (bool, error) {
	if m.currency != other.currency {
		return false, errors.New("domain_error: currency mismatch")
	}
	return m.amount >= other.amount, nil
}
