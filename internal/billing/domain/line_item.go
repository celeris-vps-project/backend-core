package domain

import "errors"

// LineItem represents a billable unit on an invoice.
type LineItem struct {
	id          string
	description string
	quantity    int64
	unitPrice   Money
}

func NewLineItem(id, description string, quantity int64, unitPrice Money) (LineItem, error) {
	if id == "" {
		return LineItem{}, errors.New("domain_error: line item id is required")
	}
	if description == "" {
		return LineItem{}, errors.New("domain_error: description is required")
	}
	if quantity <= 0 {
		return LineItem{}, errors.New("domain_error: quantity must be > 0")
	}
	if unitPrice.Currency() == "" {
		return LineItem{}, errors.New("domain_error: unit price currency is required")
	}
	return LineItem{id: id, description: description, quantity: quantity, unitPrice: unitPrice}, nil
}

func (l LineItem) ID() string          { return l.id }
func (l LineItem) Description() string { return l.description }
func (l LineItem) Quantity() int64     { return l.quantity }
func (l LineItem) UnitPrice() Money    { return l.unitPrice }

func (l LineItem) Total() (Money, error) {
	return l.unitPrice.Mul(l.quantity)
}
