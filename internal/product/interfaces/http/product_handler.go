package http

import (
	"backend-core/internal/product/app"
	"backend-core/internal/product/domain"
	"context"

	hz_app "github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/common/utils"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
)

type CreateProductRequest struct {
	Name         string `json:"name" vd:"len($)>0"`
	Slug         string `json:"slug" vd:"len($)>0"`
	Location     string `json:"location" vd:"len($)>0"`
	RegionID     string `json:"region_id"` // optional — binds product to a resource pool
	CPU          int    `json:"cpu" vd:"$>0"`
	MemoryMB     int    `json:"memory_mb" vd:"$>0"`
	DiskGB       int    `json:"disk_gb" vd:"$>0"`
	BandwidthGB  int    `json:"bandwidth_gb"`
	PriceAmount  int64  `json:"price_amount" vd:"$>0"`
	Currency     string `json:"currency" vd:"len($)>0"`
	BillingCycle string `json:"billing_cycle" vd:"len($)>0"`
	TotalSlots   int    `json:"total_slots"`
}

type PurchaseProductRequest struct {
	ProductID string `json:"product_id" vd:"len($)>0"`
	OrderID   string `json:"order_id" vd:"len($)>0"`
	Hostname  string `json:"hostname" vd:"len($)>0"`
	OS        string `json:"os" vd:"len($)>0"`
}

type UpdatePriceRequest struct {
	Amount   int64  `json:"amount" vd:"$>0"`
	Currency string `json:"currency" vd:"len($)>0"`
}

type AdjustStockRequest struct {
	TotalSlots int  `json:"total_slots" vd:"$>=-1"`
	Confirmed  bool `json:"confirmed"` // true = admin confirmed the over-sell warning
}

type SetRegionRequest struct {
	RegionID string `json:"region_id" vd:"len($)>0"`
}

type ProductResponse struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Slug           string `json:"slug"`
	Location       string `json:"location"`
	RegionID       string `json:"region_id,omitempty"`
	CPU            int    `json:"cpu"`
	MemoryMB       int    `json:"memory_mb"`
	DiskGB         int    `json:"disk_gb"`
	BandwidthGB    int    `json:"bandwidth_gb"`
	PriceAmount    int64  `json:"price_amount"`
	Currency       string `json:"currency"`
	BillingCycle   string `json:"billing_cycle"`
	Enabled        bool   `json:"enabled"`
	SortOrder      int    `json:"sort_order"`
	TotalSlots     int    `json:"total_slots"`
	SoldSlots      int    `json:"sold_slots"`
	AvailableSlots int    `json:"available_slots"`
}

// AdjustStockResponse includes an optional warning for the admin frontend.
type AdjustStockResponse struct {
	Data                 ProductResponse `json:"data"`
	Warning              bool            `json:"warning,omitempty"`
	WarningMessage       string          `json:"warning_message,omitempty"`
	PhysicalAvailable    int             `json:"physical_available,omitempty"`
	RequiresConfirmation bool            `json:"requires_confirmation,omitempty"`
	Saved                bool            `json:"saved"`
}

type ProductHandler struct{ svc *app.ProductAppService }

func NewProductHandler(svc *app.ProductAppService) *ProductHandler {
	return &ProductHandler{svc: svc}
}

func (h *ProductHandler) Create(ctx context.Context, c *hz_app.RequestContext) {
	var req CreateProductRequest
	if err := c.BindAndValidate(&req); err != nil {
		c.JSON(consts.StatusBadRequest, utils.H{"error": err.Error()})
		return
	}
	p, err := h.svc.CreateProduct(req.Name, req.Slug, req.Location, req.RegionID, req.CPU, req.MemoryMB, req.DiskGB, req.BandwidthGB, req.PriceAmount, req.Currency, domain.BillingCycle(req.BillingCycle), req.TotalSlots)
	if err != nil {
		c.JSON(consts.StatusUnprocessableEntity, utils.H{"error": err.Error()})
		return
	}
	c.JSON(consts.StatusCreated, utils.H{"data": toProductResp(p)})
}

// Purchase handles a customer purchasing a product. This consumes a
// commercial slot and publishes a ProductPurchasedEvent for the Node
// domain to handle physical provisioning.
func (h *ProductHandler) Purchase(ctx context.Context, c *hz_app.RequestContext) {
	customerID, _ := c.Get("current_user_id")
	if customerID == nil || customerID.(string) == "" {
		c.JSON(consts.StatusUnauthorized, utils.H{"error": "unauthorized"})
		return
	}
	var req PurchaseProductRequest
	if err := c.BindAndValidate(&req); err != nil {
		c.JSON(consts.StatusBadRequest, utils.H{"error": err.Error()})
		return
	}
	p, err := h.svc.PurchaseProduct(req.ProductID, customerID.(string), req.OrderID, req.Hostname, req.OS)
	if err != nil {
		c.JSON(consts.StatusUnprocessableEntity, utils.H{"error": err.Error()})
		return
	}
	c.JSON(consts.StatusOK, utils.H{"data": toProductResp(p)})
}

func (h *ProductHandler) GetByID(ctx context.Context, c *hz_app.RequestContext) {
	p, err := h.svc.GetProduct(c.Param("id"))
	if err != nil {
		c.JSON(consts.StatusNotFound, utils.H{"error": err.Error()})
		return
	}
	c.JSON(consts.StatusOK, utils.H{"data": toProductResp(p)})
}

func (h *ProductHandler) List(ctx context.Context, c *hz_app.RequestContext) {
	products, err := h.svc.ListEnabled()
	if err != nil {
		c.JSON(consts.StatusInternalServerError, utils.H{"error": err.Error()})
		return
	}
	list := make([]ProductResponse, len(products))
	for i, p := range products {
		list[i] = toProductResp(p)
	}
	c.JSON(consts.StatusOK, utils.H{"data": list})
}

func (h *ProductHandler) ListAll(ctx context.Context, c *hz_app.RequestContext) {
	products, err := h.svc.ListAll()
	if err != nil {
		c.JSON(consts.StatusInternalServerError, utils.H{"error": err.Error()})
		return
	}
	list := make([]ProductResponse, len(products))
	for i, p := range products {
		list[i] = toProductResp(p)
	}
	c.JSON(consts.StatusOK, utils.H{"data": list})
}

func (h *ProductHandler) Enable(ctx context.Context, c *hz_app.RequestContext) {
	if err := h.svc.EnableProduct(c.Param("id")); err != nil {
		c.JSON(consts.StatusUnprocessableEntity, utils.H{"error": err.Error()})
		return
	}
	p, _ := h.svc.GetProduct(c.Param("id"))
	c.JSON(consts.StatusOK, utils.H{"data": toProductResp(p)})
}

func (h *ProductHandler) Disable(ctx context.Context, c *hz_app.RequestContext) {
	if err := h.svc.DisableProduct(c.Param("id")); err != nil {
		c.JSON(consts.StatusUnprocessableEntity, utils.H{"error": err.Error()})
		return
	}
	p, _ := h.svc.GetProduct(c.Param("id"))
	c.JSON(consts.StatusOK, utils.H{"data": toProductResp(p)})
}

func (h *ProductHandler) UpdatePrice(ctx context.Context, c *hz_app.RequestContext) {
	var req UpdatePriceRequest
	if err := c.BindAndValidate(&req); err != nil {
		c.JSON(consts.StatusBadRequest, utils.H{"error": err.Error()})
		return
	}
	if err := h.svc.UpdatePrice(c.Param("id"), req.Amount, req.Currency); err != nil {
		c.JSON(consts.StatusUnprocessableEntity, utils.H{"error": err.Error()})
		return
	}
	p, _ := h.svc.GetProduct(c.Param("id"))
	c.JSON(consts.StatusOK, utils.H{"data": toProductResp(p)})
}

// AdjustStock handles admin restocking with a soft-limit warning.
// If the requested stock exceeds physical capacity and confirmed=false,
// the response includes a warning payload for the frontend to show a
// confirmation modal. The frontend must re-call with confirmed=true.
func (h *ProductHandler) AdjustStock(ctx context.Context, c *hz_app.RequestContext) {
	var req AdjustStockRequest
	if err := c.BindAndValidate(&req); err != nil {
		c.JSON(consts.StatusBadRequest, utils.H{"error": err.Error()})
		return
	}
	result, err := h.svc.AdjustStock(c.Param("id"), req.TotalSlots, req.Confirmed)
	if err != nil {
		c.JSON(consts.StatusUnprocessableEntity, utils.H{"error": err.Error()})
		return
	}

	resp := AdjustStockResponse{
		Data:                 toProductResp(result.Product),
		Warning:              result.Warning,
		WarningMessage:       result.WarningMessage,
		PhysicalAvailable:    result.PhysicalAvailable,
		RequiresConfirmation: result.RequiresConfirmation,
		Saved:                !result.RequiresConfirmation || req.Confirmed,
	}
	c.JSON(consts.StatusOK, resp)
}

// SetRegion binds a product to a region (resource pool).
func (h *ProductHandler) SetRegion(ctx context.Context, c *hz_app.RequestContext) {
	var req SetRegionRequest
	if err := c.BindAndValidate(&req); err != nil {
		c.JSON(consts.StatusBadRequest, utils.H{"error": err.Error()})
		return
	}
	if err := h.svc.SetRegion(c.Param("id"), req.RegionID); err != nil {
		c.JSON(consts.StatusUnprocessableEntity, utils.H{"error": err.Error()})
		return
	}
	p, _ := h.svc.GetProduct(c.Param("id"))
	c.JSON(consts.StatusOK, utils.H{"data": toProductResp(p)})
}

func toProductResp(p *domain.Product) ProductResponse {
	return ProductResponse{
		ID: p.ID(), Name: p.Name(), Slug: p.Slug(), Location: p.Location(),
		RegionID: p.RegionID(),
		CPU:      p.CPU(), MemoryMB: p.MemoryMB(), DiskGB: p.DiskGB(), BandwidthGB: p.BandwidthGB(),
		PriceAmount: p.PriceAmount(), Currency: p.Currency(),
		BillingCycle: string(p.BillingCycle()), Enabled: p.Enabled(), SortOrder: p.SortOrder(),
		TotalSlots: p.TotalSlots(), SoldSlots: p.SoldSlots(), AvailableSlots: p.AvailableSlots(),
	}
}
