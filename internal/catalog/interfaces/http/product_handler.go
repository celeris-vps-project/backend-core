package http

import (
	"backend-core/internal/catalog/app"
	"backend-core/internal/catalog/domain"
	"backend-core/pkg/apperr"
	"backend-core/pkg/authn"
	"context"
	"strings"

	hz_app "github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/common/utils"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
)

type CreateProductRequest struct {
	Name           string `json:"name" vd:"len($)>0"`
	Slug           string `json:"slug" vd:"len($)>0"`
	Location       string `json:"location"`
	RegionID       string `json:"region_id"`
	ResourcePoolID string `json:"resource_pool_id"`
	NetworkMode    string `json:"network_mode"`
	CPU            int    `json:"cpu" vd:"$>0"`
	MemoryMB       int    `json:"memory_mb" vd:"$>0"`
	DiskGB         int    `json:"disk_gb" vd:"$>0"`
	BandwidthGB    int    `json:"bandwidth_gb"`
	PriceAmount    int64  `json:"price_amount" vd:"$>0"`
	Currency       string `json:"currency" vd:"len($)>0"`
	BillingCycle   string `json:"billing_cycle" vd:"len($)>0"`
	TotalSlots     int    `json:"total_slots"`
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
	Confirmed  bool `json:"confirmed"`
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
	ResourcePoolID string `json:"resource_pool_id,omitempty"`
	NetworkMode    string `json:"network_mode"`
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
	IsUnlimited    bool   `json:"is_unlimited"`
}

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
		c.JSON(consts.StatusBadRequest, apperr.Resp(apperr.CodeInvalidParams, err.Error()))
		return
	}
	p, err := h.svc.CreateProduct(ctx, req.Name, req.Slug, req.Location, req.RegionID, req.ResourcePoolID, req.NetworkMode, req.CPU, req.MemoryMB, req.DiskGB, req.BandwidthGB, req.PriceAmount, req.Currency, domain.BillingCycle(req.BillingCycle), req.TotalSlots)
	if err != nil {
		c.JSON(consts.StatusUnprocessableEntity, apperr.Resp(classifyProductError(err), err.Error()))
		return
	}
	c.JSON(consts.StatusCreated, utils.H{"data": toProductResp(p)})
}

func (h *ProductHandler) Purchase(ctx context.Context, c *hz_app.RequestContext) {
	uid, ok := authn.UserID(c)
	if !ok {
		c.JSON(consts.StatusUnauthorized, apperr.Resp(apperr.CodeUnauthorized, "unauthorized"))
		return
	}
	var req PurchaseProductRequest
	if err := c.BindAndValidate(&req); err != nil {
		c.JSON(consts.StatusBadRequest, apperr.Resp(apperr.CodeInvalidParams, err.Error()))
		return
	}
	p, err := h.svc.PurchaseProduct(ctx, req.ProductID, uid.String(), req.OrderID, "", req.Hostname, req.OS)
	if err != nil {
		c.JSON(consts.StatusUnprocessableEntity, apperr.Resp(classifyProductError(err), err.Error()))
		return
	}
	c.JSON(consts.StatusOK, utils.H{"data": toProductResp(p)})
}

func (h *ProductHandler) GetByID(ctx context.Context, c *hz_app.RequestContext) {
	p, err := h.svc.GetProduct(ctx, c.Param("id"))
	if err != nil {
		c.JSON(consts.StatusNotFound, apperr.Resp(apperr.CodeProductNotFound, err.Error()))
		return
	}
	c.JSON(consts.StatusOK, utils.H{"data": toProductResp(p)})
}

func (h *ProductHandler) List(ctx context.Context, c *hz_app.RequestContext) {
	products, err := h.svc.ListEnabled(ctx)
	if err != nil {
		c.JSON(consts.StatusInternalServerError, apperr.Resp(apperr.CodeInternalError, err.Error()))
		return
	}
	list := make([]ProductResponse, len(products))
	for i, p := range products {
		list[i] = toProductResp(p)
	}
	c.JSON(consts.StatusOK, utils.H{"data": list})
}

func (h *ProductHandler) ListAll(ctx context.Context, c *hz_app.RequestContext) {
	products, err := h.svc.ListAll(ctx)
	if err != nil {
		c.JSON(consts.StatusInternalServerError, apperr.Resp(apperr.CodeInternalError, err.Error()))
		return
	}
	list := make([]ProductResponse, len(products))
	for i, p := range products {
		list[i] = toProductResp(p)
	}
	c.JSON(consts.StatusOK, utils.H{"data": list})
}

func (h *ProductHandler) Enable(ctx context.Context, c *hz_app.RequestContext) {
	if err := h.svc.EnableProduct(ctx, c.Param("id")); err != nil {
		c.JSON(consts.StatusUnprocessableEntity, apperr.Resp(classifyProductError(err), err.Error()))
		return
	}
	p, _ := h.svc.GetProduct(ctx, c.Param("id"))
	c.JSON(consts.StatusOK, utils.H{"data": toProductResp(p)})
}

func (h *ProductHandler) Disable(ctx context.Context, c *hz_app.RequestContext) {
	if err := h.svc.DisableProduct(ctx, c.Param("id")); err != nil {
		c.JSON(consts.StatusUnprocessableEntity, apperr.Resp(classifyProductError(err), err.Error()))
		return
	}
	p, _ := h.svc.GetProduct(ctx, c.Param("id"))
	c.JSON(consts.StatusOK, utils.H{"data": toProductResp(p)})
}

func (h *ProductHandler) UpdatePrice(ctx context.Context, c *hz_app.RequestContext) {
	var req UpdatePriceRequest
	if err := c.BindAndValidate(&req); err != nil {
		c.JSON(consts.StatusBadRequest, apperr.Resp(apperr.CodeInvalidParams, err.Error()))
		return
	}
	if err := h.svc.UpdatePrice(ctx, c.Param("id"), req.Amount, req.Currency); err != nil {
		c.JSON(consts.StatusUnprocessableEntity, apperr.Resp(classifyProductError(err), err.Error()))
		return
	}
	p, _ := h.svc.GetProduct(ctx, c.Param("id"))
	c.JSON(consts.StatusOK, utils.H{"data": toProductResp(p)})
}

func (h *ProductHandler) AdjustStock(ctx context.Context, c *hz_app.RequestContext) {
	var req AdjustStockRequest
	if err := c.BindAndValidate(&req); err != nil {
		c.JSON(consts.StatusBadRequest, apperr.Resp(apperr.CodeInvalidParams, err.Error()))
		return
	}
	result, err := h.svc.AdjustStock(ctx, c.Param("id"), req.TotalSlots, req.Confirmed)
	if err != nil {
		c.JSON(consts.StatusUnprocessableEntity, apperr.Resp(classifyProductError(err), err.Error()))
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

func (h *ProductHandler) SetRegion(ctx context.Context, c *hz_app.RequestContext) {
	var req SetRegionRequest
	if err := c.BindAndValidate(&req); err != nil {
		c.JSON(consts.StatusBadRequest, apperr.Resp(apperr.CodeInvalidParams, err.Error()))
		return
	}
	if err := h.svc.SetRegion(ctx, c.Param("id"), req.RegionID); err != nil {
		c.JSON(consts.StatusUnprocessableEntity, apperr.Resp(classifyProductError(err), err.Error()))
		return
	}
	p, _ := h.svc.GetProduct(ctx, c.Param("id"))
	c.JSON(consts.StatusOK, utils.H{"data": toProductResp(p)})
}

func toProductResp(p *domain.Product) ProductResponse {
	return ProductResponse{
		ID: p.ID(), Name: p.Name(), Slug: p.Slug(), Location: p.Location(),
		RegionID: p.RegionID(), ResourcePoolID: p.ResourcePoolID(), NetworkMode: p.NetworkMode(),
		CPU: p.CPU(), MemoryMB: p.MemoryMB(), DiskGB: p.DiskGB(), BandwidthGB: p.BandwidthGB(),
		PriceAmount: p.PriceAmount(), Currency: p.Currency(),
		BillingCycle: string(p.BillingCycle()), Enabled: p.Enabled(), SortOrder: p.SortOrder(),
		TotalSlots: p.TotalSlots(), SoldSlots: p.SoldSlots(), AvailableSlots: p.AvailableSlots(),
		IsUnlimited: p.IsUnlimited(),
	}
}

// classifyProductError maps product domain errors to an error code.
func classifyProductError(err error) string {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "not found"):
		return apperr.CodeProductNotFound
	case strings.Contains(msg, "no available slots"):
		return apperr.CodeNoAvailableSlots
	case strings.Contains(msg, "total slots"):
		return apperr.CodeSlotConflict
	case strings.Contains(msg, "currency"):
		return apperr.CodeCurrencyMismatch
	default:
		return apperr.CodeInvalidStateTransition
	}
}
