package http

import (
	"backend-core/internal/product/app"
	"backend-core/internal/product/domain"
	"context"

	hz_app "github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/common/utils"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
)

// ---- Request DTOs ----

type CreateGroupRequest struct {
	Name        string `json:"name" vd:"len($)>0"`
	Description string `json:"description"`
	SortOrder   int    `json:"sort_order"`
}

type UpdateGroupRequest struct {
	Name        string `json:"name" vd:"len($)>0"`
	Description string `json:"description"`
	SortOrder   int    `json:"sort_order"`
}

// ---- Response DTOs ----

type GroupResponse struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	SortOrder   int    `json:"sort_order"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

// ---- Handler ----

type GroupHandler struct{ svc *app.GroupAppService }

func NewGroupHandler(svc *app.GroupAppService) *GroupHandler {
	return &GroupHandler{svc: svc}
}

// POST /groups
func (h *GroupHandler) Create(ctx context.Context, c *hz_app.RequestContext) {
	var req CreateGroupRequest
	if err := c.BindAndValidate(&req); err != nil {
		c.JSON(consts.StatusBadRequest, utils.H{"error": err.Error()})
		return
	}
	g, err := h.svc.CreateGroup(req.Name, req.Description, req.SortOrder)
	if err != nil {
		c.JSON(consts.StatusUnprocessableEntity, utils.H{"error": err.Error()})
		return
	}
	c.JSON(consts.StatusCreated, utils.H{"data": toGroupResp(g)})
}

// GET /groups/:id
func (h *GroupHandler) GetByID(ctx context.Context, c *hz_app.RequestContext) {
	g, err := h.svc.GetGroup(c.Param("id"))
	if err != nil {
		c.JSON(consts.StatusNotFound, utils.H{"error": err.Error()})
		return
	}
	c.JSON(consts.StatusOK, utils.H{"data": toGroupResp(g)})
}

// GET /groups
func (h *GroupHandler) List(ctx context.Context, c *hz_app.RequestContext) {
	groups, err := h.svc.ListGroups()
	if err != nil {
		c.JSON(consts.StatusInternalServerError, utils.H{"error": err.Error()})
		return
	}
	list := make([]GroupResponse, len(groups))
	for i, g := range groups {
		list[i] = toGroupResp(g)
	}
	c.JSON(consts.StatusOK, utils.H{"data": list})
}

// PUT /groups/:id
func (h *GroupHandler) Update(ctx context.Context, c *hz_app.RequestContext) {
	var req UpdateGroupRequest
	if err := c.BindAndValidate(&req); err != nil {
		c.JSON(consts.StatusBadRequest, utils.H{"error": err.Error()})
		return
	}
	g, err := h.svc.UpdateGroup(c.Param("id"), req.Name, req.Description, req.SortOrder)
	if err != nil {
		c.JSON(consts.StatusUnprocessableEntity, utils.H{"error": err.Error()})
		return
	}
	c.JSON(consts.StatusOK, utils.H{"data": toGroupResp(g)})
}

// DELETE /groups/:id
func (h *GroupHandler) Delete(ctx context.Context, c *hz_app.RequestContext) {
	if err := h.svc.DeleteGroup(c.Param("id")); err != nil {
		c.JSON(consts.StatusUnprocessableEntity, utils.H{"error": err.Error()})
		return
	}
	c.JSON(consts.StatusOK, utils.H{"message": "group deleted"})
}

// ---- Mapping ----

func toGroupResp(g *domain.Group) GroupResponse {
	return GroupResponse{
		ID:          g.ID(),
		Name:        g.Name(),
		Description: g.Description(),
		SortOrder:   g.SortOrder(),
		CreatedAt:   g.CreatedAt().Format("2006-01-02T15:04:05Z07:00"),
		UpdatedAt:   g.UpdatedAt().Format("2006-01-02T15:04:05Z07:00"),
	}
}
