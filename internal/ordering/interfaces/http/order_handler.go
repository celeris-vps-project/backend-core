package http

import (
	"backend-core/internal/ordering/app"
	"backend-core/internal/ordering/domain"
	"backend-core/pkg/authn"
	"context"
	"time"

	hz_app "github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/common/utils"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
)

// ---- Request / Response DTOs ----

type CreateOrderRequest struct {
	ProductID   string `json:"product_id" vd:"len($)>0"`
	InvoiceID   string `json:"invoice_id"`
	Currency    string `json:"currency" vd:"len($)>0"`
	PriceAmount int64  `json:"price_amount" vd:"$>0"`
	Hostname    string `json:"hostname" vd:"len($)>0"`
	Plan        string `json:"plan" vd:"len($)>0"`
	Region      string `json:"region" vd:"len($)>0"`
	OS          string `json:"os" vd:"len($)>0"`
	CPU         int    `json:"cpu" vd:"$>0"`
	MemoryMB    int    `json:"memory_mb" vd:"$>0"`
	DiskGB      int    `json:"disk_gb" vd:"$>0"`
}

type CancelOrderRequest struct {
	Reason string `json:"reason" vd:"len($)>0"`
}

type OrderResponse struct {
	ID           string            `json:"id"`
	CustomerID   string            `json:"customer_id"`
	ProductID    string            `json:"product_id"`
	InvoiceID    string            `json:"invoice_id"`
	Status       string            `json:"status"`
	Currency     string            `json:"currency"`
	PriceAmount  int64             `json:"price_amount"`
	VPS          VPSConfigResponse `json:"vps"`
	CreatedAt    string            `json:"created_at"`
	ActivatedAt  *string           `json:"activated_at,omitempty"`
	SuspendedAt  *string           `json:"suspended_at,omitempty"`
	CancelledAt  *string           `json:"cancelled_at,omitempty"`
	TerminatedAt *string           `json:"terminated_at,omitempty"`
	CancelReason string            `json:"cancel_reason,omitempty"`
}

type VPSConfigResponse struct {
	Hostname string `json:"hostname"`
	Plan     string `json:"plan"`
	Region   string `json:"region"`
	OS       string `json:"os"`
	CPU      int    `json:"cpu"`
	MemoryMB int    `json:"memory_mb"`
	DiskGB   int    `json:"disk_gb"`
}

// ---- Handler ----

type OrderHandler struct {
	orderApp *app.OrderAppService
}

func NewOrderHandler(orderApp *app.OrderAppService) *OrderHandler {
	return &OrderHandler{orderApp: orderApp}
}

// POST /orders
func (h *OrderHandler) Create(ctx context.Context, c *hz_app.RequestContext) {
	uid, ok := authn.UserID(c)
	if !ok {
		c.JSON(consts.StatusUnauthorized, utils.H{"error": "unauthorized: missing user identity"})
		return
	}
	customerID := uid.String()

	var req CreateOrderRequest
	if err := c.BindAndValidate(&req); err != nil {
		c.JSON(consts.StatusBadRequest, utils.H{"error": err.Error()})
		return
	}

	invoiceID := req.InvoiceID
	if invoiceID == "" {
		invoiceID = "auto-" + customerID
	}

	cfg, err := domain.NewVPSConfig(req.Hostname, req.Plan, req.Region, req.OS, req.CPU, req.MemoryMB, req.DiskGB)
	if err != nil {
		c.JSON(consts.StatusBadRequest, utils.H{"error": err.Error()})
		return
	}

	order, err := h.orderApp.CreateOrder(customerID, req.ProductID, invoiceID, cfg, req.Currency, req.PriceAmount)
	if err != nil {
		c.JSON(consts.StatusUnprocessableEntity, utils.H{"error": err.Error()})
		return
	}

	c.JSON(consts.StatusCreated, utils.H{"data": toOrderResponse(order)})
}

// GET /orders/:id
func (h *OrderHandler) GetByID(ctx context.Context, c *hz_app.RequestContext) {
	id := c.Param("id")
	order, err := h.orderApp.GetOrder(id)
	if err != nil {
		c.JSON(consts.StatusNotFound, utils.H{"error": err.Error()})
		return
	}
	c.JSON(consts.StatusOK, utils.H{"data": toOrderResponse(order)})
}

// GET /orders
func (h *OrderHandler) ListByCustomer(ctx context.Context, c *hz_app.RequestContext) {
	uid, ok := authn.UserID(c)
	if !ok {
		c.JSON(consts.StatusUnauthorized, utils.H{"error": "unauthorized: missing user identity"})
		return
	}
	customerID := uid.String()

	orders, err := h.orderApp.ListByCustomer(customerID)
	if err != nil {
		c.JSON(consts.StatusInternalServerError, utils.H{"error": err.Error()})
		return
	}

	list := make([]OrderResponse, len(orders))
	for i, o := range orders {
		list[i] = toOrderResponse(o)
	}
	c.JSON(consts.StatusOK, utils.H{"data": list})
}

// POST /orders/:id/activate
func (h *OrderHandler) Activate(ctx context.Context, c *hz_app.RequestContext) {
	id := c.Param("id")
	if err := h.orderApp.ActivateOrder(id); err != nil {
		c.JSON(consts.StatusUnprocessableEntity, utils.H{"error": err.Error()})
		return
	}
	order, err := h.orderApp.GetOrder(id)
	if err != nil {
		c.JSON(consts.StatusInternalServerError, utils.H{"error": err.Error()})
		return
	}
	c.JSON(consts.StatusOK, utils.H{"data": toOrderResponse(order)})
}

// POST /orders/:id/suspend
func (h *OrderHandler) Suspend(ctx context.Context, c *hz_app.RequestContext) {
	id := c.Param("id")
	if err := h.orderApp.SuspendOrder(id); err != nil {
		c.JSON(consts.StatusUnprocessableEntity, utils.H{"error": err.Error()})
		return
	}
	order, err := h.orderApp.GetOrder(id)
	if err != nil {
		c.JSON(consts.StatusInternalServerError, utils.H{"error": err.Error()})
		return
	}
	c.JSON(consts.StatusOK, utils.H{"data": toOrderResponse(order)})
}

// POST /orders/:id/unsuspend
func (h *OrderHandler) Unsuspend(ctx context.Context, c *hz_app.RequestContext) {
	id := c.Param("id")
	if err := h.orderApp.UnsuspendOrder(id); err != nil {
		c.JSON(consts.StatusUnprocessableEntity, utils.H{"error": err.Error()})
		return
	}
	order, err := h.orderApp.GetOrder(id)
	if err != nil {
		c.JSON(consts.StatusInternalServerError, utils.H{"error": err.Error()})
		return
	}
	c.JSON(consts.StatusOK, utils.H{"data": toOrderResponse(order)})
}

// POST /orders/:id/cancel
func (h *OrderHandler) Cancel(ctx context.Context, c *hz_app.RequestContext) {
	id := c.Param("id")
	var req CancelOrderRequest
	if err := c.BindAndValidate(&req); err != nil {
		c.JSON(consts.StatusBadRequest, utils.H{"error": err.Error()})
		return
	}
	if err := h.orderApp.CancelOrder(id, req.Reason); err != nil {
		c.JSON(consts.StatusUnprocessableEntity, utils.H{"error": err.Error()})
		return
	}
	order, err := h.orderApp.GetOrder(id)
	if err != nil {
		c.JSON(consts.StatusInternalServerError, utils.H{"error": err.Error()})
		return
	}
	c.JSON(consts.StatusOK, utils.H{"data": toOrderResponse(order)})
}

// POST /orders/:id/terminate
func (h *OrderHandler) Terminate(ctx context.Context, c *hz_app.RequestContext) {
	id := c.Param("id")
	if err := h.orderApp.TerminateOrder(id); err != nil {
		c.JSON(consts.StatusUnprocessableEntity, utils.H{"error": err.Error()})
		return
	}
	order, err := h.orderApp.GetOrder(id)
	if err != nil {
		c.JSON(consts.StatusInternalServerError, utils.H{"error": err.Error()})
		return
	}
	c.JSON(consts.StatusOK, utils.H{"data": toOrderResponse(order)})
}

// ---- Mapping helpers ----

func toOrderResponse(o *domain.Order) OrderResponse {
	cfg := o.VPSConfig()
	resp := OrderResponse{
		ID:          o.ID(),
		CustomerID:  o.CustomerID(),
		ProductID:   o.ProductID(),
		InvoiceID:   o.InvoiceID(),
		Status:      o.Status(),
		Currency:    o.Currency(),
		PriceAmount: o.PriceAmount(),
		VPS: VPSConfigResponse{
			Hostname: cfg.Hostname(),
			Plan:     cfg.Plan(),
			Region:   cfg.Region(),
			OS:       cfg.OS(),
			CPU:      cfg.CPU(),
			MemoryMB: cfg.MemoryMB(),
			DiskGB:   cfg.DiskGB(),
		},
		CreatedAt:    o.CreatedAt().Format(time.RFC3339),
		CancelReason: o.CancelReason(),
	}
	if t := o.ActivatedAt(); t != nil {
		s := t.Format(time.RFC3339)
		resp.ActivatedAt = &s
	}
	if t := o.SuspendedAt(); t != nil {
		s := t.Format(time.RFC3339)
		resp.SuspendedAt = &s
	}
	if t := o.CancelledAt(); t != nil {
		s := t.Format(time.RFC3339)
		resp.CancelledAt = &s
	}
	if t := o.TerminatedAt(); t != nil {
		s := t.Format(time.RFC3339)
		resp.TerminatedAt = &s
	}
	return resp
}
