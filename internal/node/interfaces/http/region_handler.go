package http

import (
	"backend-core/internal/node/app"
	"backend-core/internal/node/domain"
	"context"

	hz_app "github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/common/utils"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
)

// ---- Request DTOs ----

type CreateRegionRequest struct {
	Code     string `json:"code" vd:"len($)>0"`
	Name     string `json:"name" vd:"len($)>0"`
	FlagIcon string `json:"flag_icon"`
}

// ---- Response DTOs ----

type RegionResponse struct {
	ID       string `json:"id"`
	Code     string `json:"code"`
	Name     string `json:"name"`
	FlagIcon string `json:"flag_icon"`
	Status   string `json:"status"`
}

// ---- Handler ----

type RegionHandler struct{ svc *app.RegionAppService }

func NewRegionHandler(svc *app.RegionAppService) *RegionHandler {
	return &RegionHandler{svc: svc}
}

// POST /regions
func (h *RegionHandler) Create(ctx context.Context, c *hz_app.RequestContext) {
	var req CreateRegionRequest
	if err := c.BindAndValidate(&req); err != nil {
		c.JSON(consts.StatusBadRequest, utils.H{"error": err.Error()})
		return
	}
	r, err := h.svc.CreateRegion(req.Code, req.Name, req.FlagIcon)
	if err != nil {
		c.JSON(consts.StatusUnprocessableEntity, utils.H{"error": err.Error()})
		return
	}
	c.JSON(consts.StatusCreated, utils.H{"data": toRegionResp(r)})
}

// GET /regions — list active regions (for frontend dropdown / public use)
func (h *RegionHandler) ListRegions(ctx context.Context, c *hz_app.RequestContext) {
	regions, err := h.svc.ListActive()
	if err != nil {
		c.JSON(consts.StatusInternalServerError, utils.H{"error": err.Error()})
		return
	}
	list := make([]RegionResponse, len(regions))
	for i, r := range regions {
		list[i] = toRegionResp(r)
	}
	c.JSON(consts.StatusOK, utils.H{"data": list})
}

// GET /regions/all — list all regions regardless of status (admin)
func (h *RegionHandler) ListAll(ctx context.Context, c *hz_app.RequestContext) {
	regions, err := h.svc.ListAll()
	if err != nil {
		c.JSON(consts.StatusInternalServerError, utils.H{"error": err.Error()})
		return
	}
	list := make([]RegionResponse, len(regions))
	for i, r := range regions {
		list[i] = toRegionResp(r)
	}
	c.JSON(consts.StatusOK, utils.H{"data": list})
}

// GET /regions/:id
func (h *RegionHandler) GetByID(ctx context.Context, c *hz_app.RequestContext) {
	r, err := h.svc.GetRegion(c.Param("id"))
	if err != nil {
		c.JSON(consts.StatusNotFound, utils.H{"error": err.Error()})
		return
	}
	c.JSON(consts.StatusOK, utils.H{"data": toRegionResp(r)})
}

// POST /regions/:id/activate
func (h *RegionHandler) Activate(ctx context.Context, c *hz_app.RequestContext) {
	if err := h.svc.Activate(c.Param("id")); err != nil {
		c.JSON(consts.StatusUnprocessableEntity, utils.H{"error": err.Error()})
		return
	}
	r, _ := h.svc.GetRegion(c.Param("id"))
	c.JSON(consts.StatusOK, utils.H{"data": toRegionResp(r)})
}

// POST /regions/:id/deactivate
func (h *RegionHandler) Deactivate(ctx context.Context, c *hz_app.RequestContext) {
	if err := h.svc.Deactivate(c.Param("id")); err != nil {
		c.JSON(consts.StatusUnprocessableEntity, utils.H{"error": err.Error()})
		return
	}
	r, _ := h.svc.GetRegion(c.Param("id"))
	c.JSON(consts.StatusOK, utils.H{"data": toRegionResp(r)})
}

// ---- Mapping ----

func toRegionResp(r *domain.Region) RegionResponse {
	return RegionResponse{
		ID:       r.ID(),
		Code:     r.Code(),
		Name:     r.Name(),
		FlagIcon: r.FlagIcon(),
		Status:   r.Status(),
	}
}
