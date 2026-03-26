package http

import (
	"backend-core/internal/billing/app"
	"backend-core/internal/billing/domain"
	"backend-core/pkg/apperr"
	"backend-core/pkg/authn"
	"context"
	"strings"
	"time"

	hz_app "github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/common/utils"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
)

// ---- Request / Response DTOs ----

type CreateInvoiceRequest struct {
	CustomerID   string  `json:"customer_id" vd:"len($)>0"`
	Currency     string  `json:"currency" vd:"len($)>0"`
	BillingCycle string  `json:"billing_cycle"` // one_time | monthly | yearly; defaults to one_time
	PeriodStart  *string `json:"period_start"`  // RFC3339, required for recurring
	PeriodEnd    *string `json:"period_end"`    // RFC3339, required for recurring
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

type RenewInvoiceRequest struct {
	SourceInvoiceID string `json:"source_invoice_id" vd:"len($)>0"`
}

type InvoiceResponse struct {
	ID              string             `json:"id"`
	CustomerID      string             `json:"customer_id"`
	Currency        string             `json:"currency"`
	Status          string             `json:"status"`
	BillingCycle    string             `json:"billing_cycle"`
	PeriodStart     *string            `json:"period_start,omitempty"`
	PeriodEnd       *string            `json:"period_end,omitempty"`
	NextBillingDate *string            `json:"next_billing_date,omitempty"`
	Subtotal        int64              `json:"subtotal"`
	Tax             int64              `json:"tax"`
	Total           int64              `json:"total"`
	AmountPaid      int64              `json:"amount_paid"`
	IssuedAt        *string            `json:"issued_at,omitempty"`
	DueAt           *string            `json:"due_at,omitempty"`
	PaidAt          *string            `json:"paid_at,omitempty"`
	VoidReason      string             `json:"void_reason,omitempty"`
	LineItems       []LineItemResponse `json:"line_items"`
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
		c.JSON(consts.StatusBadRequest, apperr.Resp(apperr.CodeInvalidParams, err.Error()))
		return
	}

	// Parse billing cycle; default to one_time
	cycleType := req.BillingCycle
	if cycleType == "" {
		cycleType = domain.BillingCycleOneTime
	}
	billingCycle, err := domain.NewBillingCycle(cycleType)
	if err != nil {
		c.JSON(consts.StatusBadRequest, apperr.Resp(apperr.CodeInvalidParams, err.Error()))
		return
	}

	// Parse optional period dates
	var periodStart, periodEnd *time.Time
	if req.PeriodStart != nil {
		parsed, err := time.Parse(time.RFC3339, *req.PeriodStart)
		if err != nil {
			c.JSON(consts.StatusBadRequest, apperr.Resp(apperr.CodeInvalidParams, "invalid period_start format, use RFC3339"))
			return
		}
		periodStart = &parsed
	}
	if req.PeriodEnd != nil {
		parsed, err := time.Parse(time.RFC3339, *req.PeriodEnd)
		if err != nil {
			c.JSON(consts.StatusBadRequest, apperr.Resp(apperr.CodeInvalidParams, "invalid period_end format, use RFC3339"))
			return
		}
		periodEnd = &parsed
	}

	invoice, err := h.invoiceApp.CreateDraft(req.CustomerID, req.Currency, billingCycle, periodStart, periodEnd)
	if err != nil {
		c.JSON(consts.StatusUnprocessableEntity, apperr.Resp(classifyInvoiceError(err), err.Error()))
		return
	}

	c.JSON(consts.StatusCreated, utils.H{"data": toInvoiceResponse(invoice)})
}

// GET /invoices/:id
func (h *InvoiceHandler) GetByID(ctx context.Context, c *hz_app.RequestContext) {
	id := c.Param("id")
	invoice, err := h.invoiceApp.GetInvoice(id)
	if err != nil {
		c.JSON(consts.StatusNotFound, apperr.Resp(apperr.CodeInvoiceNotFound, err.Error()))
		return
	}
	c.JSON(consts.StatusOK, utils.H{"data": toInvoiceResponse(invoice)})
}

// GET /invoices — list current user's invoices (from JWT).
// GET /invoices?customer_id=xxx — admin only: list invoices for a specific customer.
func (h *InvoiceHandler) ListByCustomer(ctx context.Context, c *hz_app.RequestContext) {
	uid, ok := authn.UserID(c)
	if !ok {
		c.JSON(consts.StatusUnauthorized, apperr.Resp(apperr.CodeUnauthorized, "unauthorized"))
		return
	}

	customerID := uid.String()

	// Allow admin to query any customer's invoices via query param.
	if qp := c.Query("customer_id"); qp != "" {
		role, _ := authn.UserRole(c)
		if role != "admin" {
			c.JSON(consts.StatusForbidden, apperr.Resp(apperr.CodeForbidden, "only admin can query other customers' invoices"))
			return
		}
		customerID = qp
	}

	invoices, err := h.invoiceApp.ListByCustomer(customerID)
	if err != nil {
		c.JSON(consts.StatusInternalServerError, apperr.Resp(apperr.CodeInternalError, err.Error()))
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
		c.JSON(consts.StatusBadRequest, apperr.Resp(apperr.CodeInvalidParams, err.Error()))
		return
	}

	// Need currency from existing invoice
	invoice, err := h.invoiceApp.GetInvoice(id)
	if err != nil {
		c.JSON(consts.StatusNotFound, apperr.Resp(apperr.CodeInvoiceNotFound, err.Error()))
		return
	}

	unitPrice, err := domain.NewMoney(invoice.Currency(), req.UnitPrice)
	if err != nil {
		c.JSON(consts.StatusBadRequest, apperr.Resp(apperr.CodeInvalidParams, err.Error()))
		return
	}
	item, err := domain.NewLineItem(req.ID, req.Description, req.Quantity, unitPrice)
	if err != nil {
		c.JSON(consts.StatusBadRequest, apperr.Resp(apperr.CodeInvalidParams, err.Error()))
		return
	}
	if err := h.invoiceApp.AddLineItem(id, item); err != nil {
		c.JSON(consts.StatusUnprocessableEntity, apperr.Resp(classifyInvoiceError(err), err.Error()))
		return
	}

	// Return updated invoice
	updated, err := h.invoiceApp.GetInvoice(id)
	if err != nil {
		c.JSON(consts.StatusInternalServerError, apperr.Resp(apperr.CodeInternalError, err.Error()))
		return
	}
	c.JSON(consts.StatusOK, utils.H{"data": toInvoiceResponse(updated)})
}

// PUT /invoices/:id/tax
func (h *InvoiceHandler) SetTax(ctx context.Context, c *hz_app.RequestContext) {
	id := c.Param("id")
	var req SetTaxRequest
	if err := c.BindAndValidate(&req); err != nil {
		c.JSON(consts.StatusBadRequest, apperr.Resp(apperr.CodeInvalidParams, err.Error()))
		return
	}

	invoice, err := h.invoiceApp.GetInvoice(id)
	if err != nil {
		c.JSON(consts.StatusNotFound, apperr.Resp(apperr.CodeInvoiceNotFound, err.Error()))
		return
	}

	tax, err := domain.NewMoney(invoice.Currency(), req.Amount)
	if err != nil {
		c.JSON(consts.StatusBadRequest, apperr.Resp(apperr.CodeInvalidParams, err.Error()))
		return
	}
	if err := h.invoiceApp.SetTaxAmount(id, tax); err != nil {
		c.JSON(consts.StatusUnprocessableEntity, apperr.Resp(classifyInvoiceError(err), err.Error()))
		return
	}

	updated, err := h.invoiceApp.GetInvoice(id)
	if err != nil {
		c.JSON(consts.StatusInternalServerError, apperr.Resp(apperr.CodeInternalError, err.Error()))
		return
	}
	c.JSON(consts.StatusOK, utils.H{"data": toInvoiceResponse(updated)})
}

// POST /invoices/:id/issue
func (h *InvoiceHandler) Issue(ctx context.Context, c *hz_app.RequestContext) {
	id := c.Param("id")
	var req IssueInvoiceRequest
	if err := c.BindAndValidate(&req); err != nil {
		c.JSON(consts.StatusBadRequest, apperr.Resp(apperr.CodeInvalidParams, err.Error()))
		return
	}

	issuedAt := time.Now()
	var dueAt *time.Time
	if req.DueAt != nil {
		parsed, err := time.Parse(time.RFC3339, *req.DueAt)
		if err != nil {
			c.JSON(consts.StatusBadRequest, apperr.Resp(apperr.CodeInvalidParams, "invalid due_at format, use RFC3339"))
			return
		}
		dueAt = &parsed
	}

	if err := h.invoiceApp.IssueInvoice(id, issuedAt, dueAt); err != nil {
		c.JSON(consts.StatusUnprocessableEntity, apperr.Resp(classifyInvoiceError(err), err.Error()))
		return
	}

	updated, err := h.invoiceApp.GetInvoice(id)
	if err != nil {
		c.JSON(consts.StatusInternalServerError, apperr.Resp(apperr.CodeInternalError, err.Error()))
		return
	}
	c.JSON(consts.StatusOK, utils.H{"data": toInvoiceResponse(updated)})
}

// POST /invoices/:id/payments
func (h *InvoiceHandler) RecordPayment(ctx context.Context, c *hz_app.RequestContext) {
	id := c.Param("id")
	var req RecordPaymentRequest
	if err := c.BindAndValidate(&req); err != nil {
		c.JSON(consts.StatusBadRequest, apperr.Resp(apperr.CodeInvalidParams, err.Error()))
		return
	}

	invoice, err := h.invoiceApp.GetInvoice(id)
	if err != nil {
		c.JSON(consts.StatusNotFound, apperr.Resp(apperr.CodeInvoiceNotFound, err.Error()))
		return
	}

	amount, err := domain.NewMoney(invoice.Currency(), req.Amount)
	if err != nil {
		c.JSON(consts.StatusBadRequest, apperr.Resp(apperr.CodeInvalidParams, err.Error()))
		return
	}

	paidAt := time.Now()
	if err := h.invoiceApp.RecordPayment(id, amount, paidAt); err != nil {
		c.JSON(consts.StatusUnprocessableEntity, apperr.Resp(classifyInvoiceError(err), err.Error()))
		return
	}

	updated, err := h.invoiceApp.GetInvoice(id)
	if err != nil {
		c.JSON(consts.StatusInternalServerError, apperr.Resp(apperr.CodeInternalError, err.Error()))
		return
	}
	c.JSON(consts.StatusOK, utils.H{"data": toInvoiceResponse(updated)})
}

// POST /invoices/:id/void
func (h *InvoiceHandler) Void(ctx context.Context, c *hz_app.RequestContext) {
	id := c.Param("id")
	var req VoidInvoiceRequest
	if err := c.BindAndValidate(&req); err != nil {
		c.JSON(consts.StatusBadRequest, apperr.Resp(apperr.CodeInvalidParams, err.Error()))
		return
	}

	if err := h.invoiceApp.VoidInvoice(id, req.Reason); err != nil {
		c.JSON(consts.StatusUnprocessableEntity, apperr.Resp(classifyInvoiceError(err), err.Error()))
		return
	}

	updated, err := h.invoiceApp.GetInvoice(id)
	if err != nil {
		c.JSON(consts.StatusInternalServerError, apperr.Resp(apperr.CodeInternalError, err.Error()))
		return
	}
	c.JSON(consts.StatusOK, utils.H{"data": toInvoiceResponse(updated)})
}

// POST /invoices/renew
func (h *InvoiceHandler) Renew(ctx context.Context, c *hz_app.RequestContext) {
	var req RenewInvoiceRequest
	if err := c.BindAndValidate(&req); err != nil {
		c.JSON(consts.StatusBadRequest, apperr.Resp(apperr.CodeInvalidParams, err.Error()))
		return
	}

	renewal, err := h.invoiceApp.GenerateRenewalInvoice(req.SourceInvoiceID)
	if err != nil {
		c.JSON(consts.StatusUnprocessableEntity, apperr.Resp(classifyInvoiceError(err), err.Error()))
		return
	}

	c.JSON(consts.StatusCreated, utils.H{"data": toInvoiceResponse(renewal)})
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
		ID:           inv.ID(),
		CustomerID:   inv.CustomerID(),
		Currency:     inv.Currency(),
		Status:       inv.Status(),
		BillingCycle: inv.BillingCycle().Type(),
		Subtotal:     inv.Subtotal().Amount(),
		Tax:          inv.Tax().Amount(),
		Total:        inv.Total().Amount(),
		AmountPaid:   inv.AmountPaid().Amount(),
		VoidReason:   inv.VoidReason(),
		LineItems:    items,
	}

	if t := inv.PeriodStart(); t != nil {
		s := t.Format(time.RFC3339)
		resp.PeriodStart = &s
	}
	if t := inv.PeriodEnd(); t != nil {
		s := t.Format(time.RFC3339)
		resp.PeriodEnd = &s
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

	// Compute next billing date for recurring invoices.
	if inv.IsRecurring() && inv.PeriodEnd() != nil {
		cycle := inv.BillingCycle()
		nextStart, _ := cycle.NextPeriod(*inv.PeriodEnd())
		s := nextStart.Format(time.RFC3339)
		resp.NextBillingDate = &s
	}

	return resp
}

// classifyInvoiceError maps invoice domain errors to an error code.
func classifyInvoiceError(err error) string {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "not found"):
		return apperr.CodeInvoiceNotFound
	case strings.Contains(msg, "currency mismatch"):
		return apperr.CodeCurrencyMismatch
	case strings.Contains(msg, "line item id already exists"):
		return apperr.CodeDuplicateLineItem
	case strings.Contains(msg, "paid invoices cannot be voided"):
		return apperr.CodeAlreadyPaid
	case strings.Contains(msg, "only draft"),
		strings.Contains(msg, "only issued"),
		strings.Contains(msg, "only paid"),
		strings.Contains(msg, "only recurring"),
		strings.Contains(msg, "already void"),
		strings.Contains(msg, "invoice total must be"),
		strings.Contains(msg, "at least one line item"):
		return apperr.CodeInvoiceInvalidState
	default:
		return apperr.CodeInvoiceInvalidState
	}
}
