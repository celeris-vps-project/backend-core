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
	CPU          int    `json:"cpu" vd:"$>0"`
	MemoryMB     int    `json:"memory_mb" vd:"$>0"`
	DiskGB       int    `json:"disk_gb" vd:"$>0"`
	BandwidthGB  int    `json:"bandwidth_gb"`
	PriceAmount  int64  `json:"price_amount" vd:"$>0"`
	Currency     string `json:"currency" vd:"len($)>0"`
	BillingCycle string `json:"billing_cycle" vd:"len($)>0"`
}

type UpdatePriceRequest struct {
	Amount   int64  `json:"amount" vd:"$>0"`
	Currency string `json:"currency" vd:"len($)>0"`
}

type ProductResponse struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Slug         string `json:"slug"`
	CPU          int    `json:"cpu"`
	MemoryMB     int    `json:"memory_mb"`
	DiskGB       int    `json:"disk_gb"`
	BandwidthGB  int    `json:"bandwidth_gb"`
	PriceAmount  int64  `json:"price_amount"`
	Currency     string `json:"currency"`
	BillingCycle string `json:"billing_cycle"`
	Enabled      bool   `json:"enabled"`
	SortOrder    int    `json:"sort_order"`
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
	p, err := h.svc.CreateProduct(req.Name, req.Slug, req.CPU, req.MemoryMB, req.DiskGB, req.BandwidthGB, req.PriceAmount, req.Currency, domain.BillingCycle(req.BillingCycle))
	if err != nil {
		c.JSON(consts.StatusUnprocessableEntity, utils.H{"error": err.Error()})
		return
	}
	c.JSON(consts.StatusCreated, utils.H{"data": toProductResp(p)})
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

func toProductResp(p *domain.Product) ProductResponse {
	return ProductResponse{
		ID: p.ID(), Name: p.Name(), Slug: p.Slug(),
		CPU: p.CPU(), MemoryMB: p.MemoryMB(), DiskGB: p.DiskGB(), BandwidthGB: p.BandwidthGB(),
		PriceAmount: p.PriceAmount(), Currency: p.Currency(),
		BillingCycle: string(p.BillingCycle()), Enabled: p.Enabled(), SortOrder: p.SortOrder(),
	}
}
