package http

import (
	"backend-core/internal/instance/app"
	"backend-core/internal/instance/domain"
	"backend-core/pkg/apperr"
	"backend-core/pkg/authn"
	"context"
	"strings"
	"time"

	hz_app "github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/common/utils"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
)

// ---- Request DTOs ----

type PurchaseInstanceRequest struct {
	OrderID  string `json:"order_id" vd:"len($)>0"`
	Region   string `json:"region" vd:"len($)>0"`
	Hostname string `json:"hostname" vd:"len($)>0"`
	Plan     string `json:"plan" vd:"len($)>0"`
	OS       string `json:"os" vd:"len($)>0"`
	CPU      int    `json:"cpu" vd:"$>0"`
	MemoryMB int    `json:"memory_mb" vd:"$>0"`
	DiskGB   int    `json:"disk_gb" vd:"$>0"`
}

type AssignIPRequest struct {
	IPv4 string `json:"ipv4"`
	IPv6 string `json:"ipv6"`
}

// ---- Response DTOs ----

type InstanceResponse struct {
	ID           string  `json:"id"`
	CustomerID   string  `json:"customer_id"`
	OrderID      string  `json:"order_id"`
	NodeID       string  `json:"node_id"`
	Hostname     string  `json:"hostname"`
	Plan         string  `json:"plan"`
	OS           string  `json:"os"`
	CPU          int     `json:"cpu"`
	MemoryMB     int     `json:"memory_mb"`
	DiskGB       int     `json:"disk_gb"`
	IPv4         string  `json:"ipv4,omitempty"`
	IPv6         string  `json:"ipv6,omitempty"`
	Status       string  `json:"status"`
	NetworkMode  string  `json:"network_mode,omitempty"` // "dedicated" or "nat"
	NATPort      int     `json:"nat_port,omitempty"`     // NAT mode: SSH port on host
	CreatedAt    string  `json:"created_at"`
	StartedAt    *string `json:"started_at,omitempty"`
	StoppedAt    *string `json:"stopped_at,omitempty"`
	SuspendedAt  *string `json:"suspended_at,omitempty"`
	TerminatedAt *string `json:"terminated_at,omitempty"`
}

// ---- Handler ----

type InstanceHandler struct {
	svc *app.InstanceAppService
}

func NewInstanceHandler(svc *app.InstanceAppService) *InstanceHandler {
	return &InstanceHandler{svc: svc}
}

// ==================== Instance endpoints ====================

// POST /instances
func (h *InstanceHandler) Purchase(ctx context.Context, c *hz_app.RequestContext) {
	uid, ok := authn.UserID(c)
	if !ok {
		c.JSON(consts.StatusUnauthorized, apperr.Resp(apperr.CodeUnauthorized, "unauthorized"))
		return
	}
	var req PurchaseInstanceRequest
	if err := c.BindAndValidate(&req); err != nil {
		c.JSON(consts.StatusBadRequest, apperr.Resp(apperr.CodeInvalidParams, err.Error()))
		return
	}
	inst, err := h.svc.PurchaseInstance(
		uid.String(), req.OrderID, req.Region,
		req.Hostname, req.Plan, req.OS,
		req.CPU, req.MemoryMB, req.DiskGB,
	)
	if err != nil {
		c.JSON(consts.StatusUnprocessableEntity, apperr.Resp(classifyInstanceError(err), err.Error()))
		return
	}
	c.JSON(consts.StatusCreated, utils.H{"data": toInstResp(inst)})
}

// GET /instances
func (h *InstanceHandler) ListByCustomer(ctx context.Context, c *hz_app.RequestContext) {
	uid, ok := authn.UserID(c)
	if !ok {
		c.JSON(consts.StatusUnauthorized, apperr.Resp(apperr.CodeUnauthorized, "unauthorized"))
		return
	}
	insts, err := h.svc.ListByCustomer(uid.String())
	if err != nil {
		c.JSON(consts.StatusInternalServerError, apperr.Resp(apperr.CodeInternalError, err.Error()))
		return
	}
	list := make([]InstanceResponse, len(insts))
	for i, inst := range insts {
		list[i] = toInstResp(inst)
	}
	c.JSON(consts.StatusOK, utils.H{"data": list})
}

// GET /instances/:id
func (h *InstanceHandler) GetByID(ctx context.Context, c *hz_app.RequestContext) {
	inst, err := h.svc.GetInstance(c.Param("id"))
	if err != nil {
		c.JSON(consts.StatusNotFound, apperr.Resp(apperr.CodeInstanceNotFound, err.Error()))
		return
	}
	c.JSON(consts.StatusOK, utils.H{"data": toInstResp(inst)})
}

// POST /instances/:id/start
func (h *InstanceHandler) Start(ctx context.Context, c *hz_app.RequestContext) {
	if err := h.svc.StartInstance(c.Param("id")); err != nil {
		c.JSON(consts.StatusUnprocessableEntity, apperr.Resp(classifyInstanceError(err), err.Error()))
		return
	}
	inst, _ := h.svc.GetInstance(c.Param("id"))
	c.JSON(consts.StatusOK, utils.H{"data": toInstResp(inst)})
}

// POST /instances/:id/stop
func (h *InstanceHandler) Stop(ctx context.Context, c *hz_app.RequestContext) {
	if err := h.svc.StopInstance(c.Param("id")); err != nil {
		c.JSON(consts.StatusUnprocessableEntity, apperr.Resp(classifyInstanceError(err), err.Error()))
		return
	}
	inst, _ := h.svc.GetInstance(c.Param("id"))
	c.JSON(consts.StatusOK, utils.H{"data": toInstResp(inst)})
}

// POST /instances/:id/suspend
func (h *InstanceHandler) Suspend(ctx context.Context, c *hz_app.RequestContext) {
	if err := h.svc.SuspendInstance(c.Param("id")); err != nil {
		c.JSON(consts.StatusUnprocessableEntity, apperr.Resp(classifyInstanceError(err), err.Error()))
		return
	}
	inst, _ := h.svc.GetInstance(c.Param("id"))
	c.JSON(consts.StatusOK, utils.H{"data": toInstResp(inst)})
}

// POST /instances/:id/unsuspend
func (h *InstanceHandler) Unsuspend(ctx context.Context, c *hz_app.RequestContext) {
	if err := h.svc.UnsuspendInstance(c.Param("id")); err != nil {
		c.JSON(consts.StatusUnprocessableEntity, apperr.Resp(classifyInstanceError(err), err.Error()))
		return
	}
	inst, _ := h.svc.GetInstance(c.Param("id"))
	c.JSON(consts.StatusOK, utils.H{"data": toInstResp(inst)})
}

// POST /instances/:id/terminate
func (h *InstanceHandler) Terminate(ctx context.Context, c *hz_app.RequestContext) {
	if err := h.svc.TerminateInstance(c.Param("id")); err != nil {
		c.JSON(consts.StatusUnprocessableEntity, apperr.Resp(classifyInstanceError(err), err.Error()))
		return
	}
	inst, _ := h.svc.GetInstance(c.Param("id"))
	c.JSON(consts.StatusOK, utils.H{"data": toInstResp(inst)})
}

// PUT /instances/:id/ip
func (h *InstanceHandler) AssignIP(ctx context.Context, c *hz_app.RequestContext) {
	var req AssignIPRequest
	if err := c.BindAndValidate(&req); err != nil {
		c.JSON(consts.StatusBadRequest, apperr.Resp(apperr.CodeInvalidParams, err.Error()))
		return
	}
	if err := h.svc.AssignIP(c.Param("id"), req.IPv4, req.IPv6); err != nil {
		c.JSON(consts.StatusUnprocessableEntity, apperr.Resp(classifyInstanceError(err), err.Error()))
		return
	}
	inst, _ := h.svc.GetInstance(c.Param("id"))
	c.JSON(consts.StatusOK, utils.H{"data": toInstResp(inst)})
}

// ---- Mapping ----

func toInstResp(i *domain.Instance) InstanceResponse {
	resp := InstanceResponse{
		ID: i.ID(), CustomerID: i.CustomerID(), OrderID: i.OrderID(), NodeID: i.NodeID(),
		Hostname: i.Hostname(), Plan: i.Plan(), OS: i.OS(),
		CPU: i.CPU(), MemoryMB: i.MemoryMB(), DiskGB: i.DiskGB(),
		IPv4: i.IPv4(), IPv6: i.IPv6(), Status: i.Status(),
		NetworkMode: i.NetworkMode(), NATPort: i.NATPort(),
		CreatedAt: i.CreatedAt().Format(time.RFC3339),
	}
	if t := i.StartedAt(); t != nil {
		s := t.Format(time.RFC3339)
		resp.StartedAt = &s
	}
	if t := i.StoppedAt(); t != nil {
		s := t.Format(time.RFC3339)
		resp.StoppedAt = &s
	}
	if t := i.SuspendedAt(); t != nil {
		s := t.Format(time.RFC3339)
		resp.SuspendedAt = &s
	}
	if t := i.TerminatedAt(); t != nil {
		s := t.Format(time.RFC3339)
		resp.TerminatedAt = &s
	}
	return resp
}

// classifyInstanceError maps instance domain errors to an error code.
func classifyInstanceError(err error) string {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "not found"):
		return apperr.CodeInstanceNotFound
	case strings.Contains(msg, "can only start"),
		strings.Contains(msg, "only running"),
		strings.Contains(msg, "only suspended"),
		strings.Contains(msg, "already suspended"),
		strings.Contains(msg, "already terminated"),
		strings.Contains(msg, "cannot be suspended"),
		strings.Contains(msg, "cannot be"):
		return apperr.CodeInvalidStateTransition
	default:
		return apperr.CodeInvalidStateTransition
	}
}
