package http

import (
	"backend-core/internal/billing/app"
	"backend-core/internal/billing/domain"
	"context"
	"time"

	hz_app "github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/common/utils"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
)

// ---- Request / Response DTOs ----

type CreateInvoiceRequest struct {
	CustomerID string `json:"customer_id" vd:"len($)>0"`
	Currency   string `json:"currency" vd:"len($)>0"`
}

type AddLineItemRequest struct {
	ID          string `json:"id" vd:"len($)>0"`
	Description string `json:"description" vd:"len($)>0"`
	Quantity    int64  `json:"quantity" vd:"$>0"`
	UnitPrice   int64  `json:"unit_price" vd:"$>=0"`
}

type SetTaxRequest struct {
	Amount int64 `json:"amount" vd:"$>=0"`
}

type IssueInvoiceRequest struct {
	DueAt *string `json:"due_at"` // RFC3339, optional
}

type RecordPaymentRequest struct {
	Amount int64 `json:"amount" vd:"$>0"`
}

type VoidInvoiceRequest struct {
	Reason string `json:"reason" vd:"len($)>0"`
}

type InvoiceResponse struct {
	ID         string             `json:"id"`
	CustomerID string             `json:"customer_id"`
	Currency   string             `json:"currency"`
	Status     string             `json:"status"`
	Subtotal   int64              `json:"subtotal"`
	Tax        int64              `json:"tax"`
	Total      int64              `json:"total"`
	AmountPaid int64              `json:"amount_paid"`
	IssuedAt   *string            `json:"issued_at,omitempty"`
	DueAt      *string            `json:"due_at,omitempty"`
	PaidAt     *string            `json:"paid_at,omitempty"`
	VoidReason string             `json:"void_reason,omitempty"`
	LineItems  []LineItemResponse `json:"line_items"`
}

type LineItemResponse struct {
	ID          string `json:"id"`
	Description string `json:"description"`
	Quantity    int64  `json:"quantity"`
	UnitPrice   int64  `json:"unit_price"`
	Total       int64  `json:"total"`
}

// ---- Handler ----

type InvoiceHandler struct {
	invoiceApp *app.InvoiceAppService
}

func NewInvoiceHandler(invoiceApp *app.InvoiceAppService) *InvoiceHandler {
	return &InvoiceHandler{invoiceApp: invoiceApp}
}

// POST /invoices
func (h *InvoiceHandler) Create(ctx context.Context, c *hz_app.RequestContext) {
	var req CreateInvoiceRequest
	if err := c.BindAndValidate(&req); err != nil {
		c.JSON(consts.StatusBadRequest, utils.H{"error": err.Error()})
		return
	}

	invoice, err := h.invoiceApp.CreateDraft(req.CustomerID, req.Currency)
	if err != nil {
		c.JSON(consts.StatusUnprocessableEntity, utils.H{"error": err.Error()})
		return
	}

	c.JSON(consts.StatusCreated, utils.H{"data": toInvoiceResponse(invoice)})
}

// GET /invoices/:id
func (h *InvoiceHandler) GetByID(ctx context.Context, c *hz_app.RequestContext) {
	id := c.Param("id")
	invoice, err := h.invoiceApp.GetInvoice(id)
	if err != nil {
		c.JSON(consts.StatusNotFound, utils.H{"error": err.Error()})
		return
	}
	c.JSON(consts.StatusOK, utils.H{"data": toInvoiceResponse(invoice)})
}

// GET /invoices?customer_id=xxx
func (h *InvoiceHandler) ListByCustomer(ctx context.Context, c *hz_app.RequestContext) {
	customerID := c.Query("customer_id")
	if customerID == "" {
		c.JSON(consts.StatusBadRequest, utils.H{"error": "customer_id query param is required"})
		return
	}

	invoices, err := h.invoiceApp.ListByCustomer(customerID)
	if err != nil {
		c.JSON(consts.StatusInternalServerError, utils.H{"error": err.Error()})
		return
	}

	list := make([]InvoiceResponse, len(invoices))
	for i, inv := range invoices {
		list[i] = toInvoiceResponse(inv)
	}
	c.JSON(consts.StatusOK, utils.H{"data": list})
}

// POST /invoices/:id/line-items
func (h *InvoiceHandler) AddLineItem(ctx context.Context, c *hz_app.RequestContext) {
	id := c.Param("id")
	var req AddLineItemRequest
	if err := c.BindAndValidate(&req); err != nil {
		c.JSON(consts.StatusBadRequest, utils.H{"error": err.Error()})
		return
	}

	// Need currency from existing invoice
	invoice, err := h.invoiceApp.GetInvoice(id)
	if err != nil {
		c.JSON(consts.StatusNotFound, utils.H{"error": err.Error()})
		return
	}

	unitPrice, err := domain.NewMoney(invoice.Currency(), req.UnitPrice)
	if err != nil {
		c.JSON(consts.StatusBadRequest, utils.H{"error": err.Error()})
		return
	}
	item, err := domain.NewLineItem(req.ID, req.Description, req.Quantity, unitPrice)
	if err != nil {
		c.JSON(consts.StatusBadRequest, utils.H{"error": err.Error()})
		return
	}
	if err := h.invoiceApp.AddLineItem(id, item); err != nil {
		c.JSON(consts.StatusUnprocessableEntity, utils.H{"error": err.Error()})
		return
	}

	// Return updated invoice
	updated, err := h.invoiceApp.GetInvoice(id)
	if err != nil {
		c.JSON(consts.StatusInternalServerError, utils.H{"error": err.Error()})
		return
	}
	c.JSON(consts.StatusOK, utils.H{"data": toInvoiceResponse(updated)})
}

// PUT /invoices/:id/tax
func (h *InvoiceHandler) SetTax(ctx context.Context, c *hz_app.RequestContext) {
	id := c.Param("id")
	var req SetTaxRequest
	if err := c.BindAndValidate(&req); err != nil {
		c.JSON(consts.StatusBadRequest, utils.H{"error": err.Error()})
		return
	}

	invoice, err := h.invoiceApp.GetInvoice(id)
	if err != nil {
		c.JSON(consts.StatusNotFound, utils.H{"error": err.Error()})
		return
	}

	tax, err := domain.NewMoney(invoice.Currency(), req.Amount)
	if err != nil {
		c.JSON(consts.StatusBadRequest, utils.H{"error": err.Error()})
		return
	}
	if err := h.invoiceApp.SetTaxAmount(id, tax); err != nil {
		c.JSON(consts.StatusUnprocessableEntity, utils.H{"error": err.Error()})
		return
	}

	updated, err := h.invoiceApp.GetInvoice(id)
	if err != nil {
		c.JSON(consts.StatusInternalServerError, utils.H{"error": err.Error()})
		return
	}
	c.JSON(consts.StatusOK, utils.H{"data": toInvoiceResponse(updated)})
}

// POST /invoices/:id/issue
func (h *InvoiceHandler) Issue(ctx context.Context, c *hz_app.RequestContext) {
	id := c.Param("id")
	var req IssueInvoiceRequest
	if err := c.BindAndValidate(&req); err != nil {
		c.JSON(consts.StatusBadRequest, utils.H{"error": err.Error()})
		return
	}

	issuedAt := time.Now()
	var dueAt *time.Time
	if req.DueAt != nil {
		parsed, err := time.Parse(time.RFC3339, *req.DueAt)
		if err != nil {
			c.JSON(consts.StatusBadRequest, utils.H{"error": "invalid due_at format, use RFC3339"})
			return
		}
		dueAt = &parsed
	}

	if err := h.invoiceApp.IssueInvoice(id, issuedAt, dueAt); err != nil {
		c.JSON(consts.StatusUnprocessableEntity, utils.H{"error": err.Error()})
		return
	}

	updated, err := h.invoiceApp.GetInvoice(id)
	if err != nil {
		c.JSON(consts.StatusInternalServerError, utils.H{"error": err.Error()})
		return
	}
	c.JSON(consts.StatusOK, utils.H{"data": toInvoiceResponse(updated)})
}

// POST /invoices/:id/payments
func (h *InvoiceHandler) RecordPayment(ctx context.Context, c *hz_app.RequestContext) {
	id := c.Param("id")
	var req RecordPaymentRequest
	if err := c.BindAndValidate(&req); err != nil {
		c.JSON(consts.StatusBadRequest, utils.H{"error": err.Error()})
		return
	}

	invoice, err := h.invoiceApp.GetInvoice(id)
	if err != nil {
		c.JSON(consts.StatusNotFound, utils.H{"error": err.Error()})
		return
	}

	amount, err := domain.NewMoney(invoice.Currency(), req.Amount)
	if err != nil {
		c.JSON(consts.StatusBadRequest, utils.H{"error": err.Error()})
		return
	}

	paidAt := time.Now()
	if err := h.invoiceApp.RecordPayment(id, amount, paidAt); err != nil {
		c.JSON(consts.StatusUnprocessableEntity, utils.H{"error": err.Error()})
		return
	}

	updated, err := h.invoiceApp.GetInvoice(id)
	if err != nil {
		c.JSON(consts.StatusInternalServerError, utils.H{"error": err.Error()})
		return
	}
	c.JSON(consts.StatusOK, utils.H{"data": toInvoiceResponse(updated)})
}

// POST /invoices/:id/void
func (h *InvoiceHandler) Void(ctx context.Context, c *hz_app.RequestContext) {
	id := c.Param("id")
	var req VoidInvoiceRequest
	if err := c.BindAndValidate(&req); err != nil {
		c.JSON(consts.StatusBadRequest, utils.H{"error": err.Error()})
		return
	}

	if err := h.invoiceApp.VoidInvoice(id, req.Reason); err != nil {
		c.JSON(consts.StatusUnprocessableEntity, utils.H{"error": err.Error()})
		return
	}

	updated, err := h.invoiceApp.GetInvoice(id)
	if err != nil {
		c.JSON(consts.StatusInternalServerError, utils.H{"error": err.Error()})
		return
	}
	c.JSON(consts.StatusOK, utils.H{"data": toInvoiceResponse(updated)})
}

// ---- Mapping helpers ----

func toInvoiceResponse(inv *domain.Invoice) InvoiceResponse {
	items := make([]LineItemResponse, len(inv.LineItems()))
	for i, li := range inv.LineItems() {
		total, _ := li.Total()
		items[i] = LineItemResponse{
			ID:          li.ID(),
			Description: li.Description(),
			Quantity:    li.Quantity(),
			UnitPrice:   li.UnitPrice().Amount(),
			Total:       total.Amount(),
		}
	}

	resp := InvoiceResponse{
		ID:         inv.ID(),
		CustomerID: inv.CustomerID(),
		Currency:   inv.Currency(),
		Status:     inv.Status(),
		Subtotal:   inv.Subtotal().Amount(),
		Tax:        inv.Tax().Amount(),
		Total:      inv.Total().Amount(),
		AmountPaid: inv.AmountPaid().Amount(),
		VoidReason: inv.VoidReason(),
		LineItems:  items,
	}

	if t := inv.IssuedAt(); t != nil {
		s := t.Format(time.RFC3339)
		resp.IssuedAt = &s
	}
	if t := inv.DueAt(); t != nil {
		s := t.Format(time.RFC3339)
		resp.DueAt = &s
	}
	if t := inv.PaidAt(); t != nil {
		s := t.Format(time.RFC3339)
		resp.PaidAt = &s
	}
	return resp
}
