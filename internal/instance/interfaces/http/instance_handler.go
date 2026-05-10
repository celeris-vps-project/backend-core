package http

import (
	"backend-core/internal/instance/app"
	"backend-core/internal/instance/domain"
	"backend-core/pkg/apperr"
	"backend-core/pkg/authn"
	"backend-core/pkg/contracts"
	"context"
	"strings"
	"time"

	hz_app "github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/common/utils"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
)

// ---- Request DTOs ----

type PurchaseInstanceRequest struct {
	OrderID     string `json:"order_id" vd:"len($)>0"`
	Region      string `json:"region" vd:"len($)>0"`
	Hostname    string `json:"hostname" vd:"len($)>0"`
	Plan        string `json:"plan" vd:"len($)>0"`
	OS          string `json:"os" vd:"len($)>0"`
	CPU         int    `json:"cpu" vd:"$>0"`
	MemoryMB    int    `json:"memory_mb" vd:"$>0"`
	DiskGB      int    `json:"disk_gb" vd:"$>0"`
	BandwidthGB int    `json:"bandwidth_gb"`
}

type AssignIPRequest struct {
	IPv4 string `json:"ipv4"`
	IPv6 string `json:"ipv6"`
}

// ---- Response DTOs ----

type InstanceResponse struct {
	ID              string                   `json:"id"`
	CustomerID      string                   `json:"customer_id"`
	OrderID         string                   `json:"order_id"`
	NodeID          string                   `json:"node_id"`
	Hostname        string                   `json:"hostname"`
	Plan            string                   `json:"plan"`
	OS              string                   `json:"os"`
	CPU             int                      `json:"cpu"`
	MemoryMB        int                      `json:"memory_mb"`
	DiskGB          int                      `json:"disk_gb"`
	BandwidthGB     int                      `json:"bandwidth_gb"`
	IPv4            string                   `json:"ipv4,omitempty"`
	IPv6            string                   `json:"ipv6,omitempty"`
	HostIP          string                   `json:"host_ip,omitempty"`
	Status          string                   `json:"status"`
	ControlStatus   string                   `json:"control_status,omitempty"`
	SuspendReason   string                   `json:"suspend_reason,omitempty"`
	RuntimeState    string                   `json:"runtime_state,omitempty"`
	RuntimeReported bool                     `json:"runtime_reported"`
	NetworkMode     string                   `json:"network_mode,omitempty"` // "dedicated" or "nat"
	NATPort         int                      `json:"nat_port,omitempty"`     // NAT mode: SSH port on host
	NATPorts        []int                    `json:"nat_ports,omitempty"`
	NATPortMappings []NATPortMappingResponse `json:"nat_port_mappings,omitempty"`
	InitialPassword string                   `json:"initial_password,omitempty"`
	CreatedAt       string                   `json:"created_at"`
	StartedAt       *string                  `json:"started_at,omitempty"`
	StoppedAt       *string                  `json:"stopped_at,omitempty"`
	SuspendedAt     *string                  `json:"suspended_at,omitempty"`
	TerminatedAt    *string                  `json:"terminated_at,omitempty"`
}

type NATPortMappingResponse struct {
	HostPort  int    `json:"host_port"`
	GuestPort int    `json:"guest_port"`
	Protocol  string `json:"protocol,omitempty"`
}

type TrafficUsageResponse struct {
	InstanceID      string                 `json:"instance_id"`
	LastEndPeriodAt string                 `json:"last_end_period_at"`
	PeriodStart     string                 `json:"period_start"`
	PeriodEnd       string                 `json:"period_end"`
	RX              uint64                 `json:"rx"`
	TX              uint64                 `json:"tx"`
	Usage           uint64                 `json:"usage"`
	BandwidthGB     int                    `json:"bandwidth_gb"`
	PeriodMax       uint64                 `json:"period_max"`
	UsagePercent    float64                `json:"usage_percent"`
	OverLimit       bool                   `json:"over_limit"`
	Daily           []TrafficDailyResponse `json:"daily"`
}

type TrafficDailyResponse struct {
	Date  string `json:"date"`
	RX    uint64 `json:"rx"`
	TX    uint64 `json:"tx"`
	Usage uint64 `json:"usage"`
}

// ---- Handler ----

type InstanceHandler struct {
	svc        *app.InstanceAppService
	trafficSvc *app.TrafficService
}

func NewInstanceHandler(svc *app.InstanceAppService, trafficSvc *app.TrafficService) *InstanceHandler {
	return &InstanceHandler{svc: svc, trafficSvc: trafficSvc}
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
	req.Hostname = strings.TrimSpace(req.Hostname)
	if req.Hostname == "" {
		c.JSON(consts.StatusBadRequest, apperr.Resp(apperr.CodeInvalidParams, "hostname is required"))
		return
	}
	inst, err := h.svc.PurchaseInstance(
		uid.String(), req.OrderID, req.Region,
		req.Hostname, req.Plan, req.OS,
		req.CPU, req.MemoryMB, req.DiskGB, req.BandwidthGB,
	)
	if err != nil {
		c.JSON(consts.StatusUnprocessableEntity, apperr.Resp(classifyInstanceError(err), err.Error()))
		return
	}
	c.JSON(consts.StatusCreated, utils.H{"data": h.toInstResp(inst)})
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
		list[i] = h.toInstResp(inst)
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
	if !canAccessInstance(c, inst.CustomerID()) {
		c.JSON(consts.StatusForbidden, apperr.Resp(apperr.CodeForbidden, "instance access denied"))
		return
	}
	c.JSON(consts.StatusOK, utils.H{"data": h.toInstResp(inst)})
}

// GET /instances/:id/traffic
func (h *InstanceHandler) TrafficUsage(ctx context.Context, c *hz_app.RequestContext) {
	id := c.Param("id")
	if !h.ensureInstanceAccess(c, id) {
		return
	}
	if h.trafficSvc == nil {
		c.JSON(consts.StatusInternalServerError, apperr.Resp(apperr.CodeInternalError, "traffic service unavailable"))
		return
	}
	usage, err := h.trafficSvc.GetInstanceTrafficUsage(id)
	if err != nil {
		c.JSON(consts.StatusInternalServerError, apperr.Resp(apperr.CodeInternalError, err.Error()))
		return
	}
	c.JSON(consts.StatusOK, utils.H{"data": toTrafficUsageResp(usage)})
}

// POST /instances/:id/start
func (h *InstanceHandler) Start(ctx context.Context, c *hz_app.RequestContext) {
	id := c.Param("id")
	if !h.ensureInstanceAccess(c, id) {
		return
	}
	if err := h.svc.StartInstance(id); err != nil {
		c.JSON(consts.StatusUnprocessableEntity, apperr.Resp(classifyInstanceError(err), err.Error()))
		return
	}
	inst, _ := h.svc.GetInstance(id)
	c.JSON(consts.StatusOK, utils.H{"data": h.toInstResp(inst)})
}

// POST /instances/:id/stop
func (h *InstanceHandler) Stop(ctx context.Context, c *hz_app.RequestContext) {
	id := c.Param("id")
	if !h.ensureInstanceAccess(c, id) {
		return
	}
	if err := h.svc.StopInstance(id); err != nil {
		c.JSON(consts.StatusUnprocessableEntity, apperr.Resp(classifyInstanceError(err), err.Error()))
		return
	}
	inst, _ := h.svc.GetInstance(id)
	c.JSON(consts.StatusOK, utils.H{"data": h.toInstResp(inst)})
}

// POST /instances/:id/suspend
func (h *InstanceHandler) Suspend(ctx context.Context, c *hz_app.RequestContext) {
	if err := h.svc.SuspendInstance(c.Param("id")); err != nil {
		c.JSON(consts.StatusUnprocessableEntity, apperr.Resp(classifyInstanceError(err), err.Error()))
		return
	}
	inst, _ := h.svc.GetInstance(c.Param("id"))
	c.JSON(consts.StatusOK, utils.H{"data": h.toInstResp(inst)})
}

// POST /instances/:id/unsuspend
func (h *InstanceHandler) Unsuspend(ctx context.Context, c *hz_app.RequestContext) {
	if err := h.svc.UnsuspendInstance(c.Param("id")); err != nil {
		c.JSON(consts.StatusUnprocessableEntity, apperr.Resp(classifyInstanceError(err), err.Error()))
		return
	}
	inst, _ := h.svc.GetInstance(c.Param("id"))
	c.JSON(consts.StatusOK, utils.H{"data": h.toInstResp(inst)})
}

// POST /instances/:id/terminate
func (h *InstanceHandler) Terminate(ctx context.Context, c *hz_app.RequestContext) {
	if err := h.svc.TerminateInstance(c.Param("id")); err != nil {
		c.JSON(consts.StatusUnprocessableEntity, apperr.Resp(classifyInstanceError(err), err.Error()))
		return
	}
	inst, _ := h.svc.GetInstance(c.Param("id"))
	c.JSON(consts.StatusOK, utils.H{"data": h.toInstResp(inst)})
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
	c.JSON(consts.StatusOK, utils.H{"data": h.toInstResp(inst)})
}

// ---- Mapping ----

func (h *InstanceHandler) ensureInstanceAccess(c *hz_app.RequestContext, instanceID string) bool {
	inst, err := h.svc.GetInstance(instanceID)
	if err != nil {
		c.JSON(consts.StatusNotFound, apperr.Resp(apperr.CodeInstanceNotFound, err.Error()))
		return false
	}
	if !canAccessInstance(c, inst.CustomerID()) {
		c.JSON(consts.StatusForbidden, apperr.Resp(apperr.CodeForbidden, "instance access denied"))
		return false
	}
	return true
}

func canAccessInstance(c *hz_app.RequestContext, customerID string) bool {
	role, _ := authn.UserRole(c)
	if role == "admin" {
		return true
	}
	uid, ok := authn.UserID(c)
	return ok && uid.String() == customerID
}

func (h *InstanceHandler) toInstResp(i *domain.Instance) InstanceResponse {
	runtimeState := h.svc.InstanceRuntimeState(i)
	resp := InstanceResponse{
		ID: i.ID(), CustomerID: i.CustomerID(), OrderID: i.OrderID(), NodeID: i.NodeID(),
		Hostname: i.Hostname(), Plan: i.Plan(), OS: i.OS(),
		CPU: i.CPU(), MemoryMB: i.MemoryMB(), DiskGB: i.DiskGB(), BandwidthGB: i.BandwidthGB(),
		IPv4: i.IPv4(), IPv6: i.IPv6(), HostIP: i.HostIP(),
		Status:          h.svc.InstanceStatus(i),
		ControlStatus:   i.ControlStatus(),
		SuspendReason:   i.SuspendReason(),
		RuntimeState:    runtimeState,
		RuntimeReported: runtimeState != "",
		NetworkMode:     i.NetworkMode(), NATPort: i.NATPort(), InitialPassword: i.InitialPassword(),
		CreatedAt: i.CreatedAt().Format(time.RFC3339),
	}
	if mappings, err := h.instanceNATPortMappings(i); err == nil {
		resp.NATPortMappings = mappings
		resp.NATPorts = natPortsFromMappings(mappings)
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

func toTrafficUsageResp(usage *domain.TrafficUsageSummary) TrafficUsageResponse {
	if usage == nil {
		return TrafficUsageResponse{}
	}
	daily := make([]TrafficDailyResponse, 0, len(usage.Daily))
	for _, row := range usage.Daily {
		daily = append(daily, TrafficDailyResponse{
			Date:  row.Date.Format("2006-01-02"),
			RX:    row.RX,
			TX:    row.TX,
			Usage: row.RX + row.TX,
		})
	}
	return TrafficUsageResponse{
		InstanceID:      usage.InstanceID,
		LastEndPeriodAt: usage.LastEndPeriodAt.Format(time.RFC3339),
		PeriodStart:     usage.PeriodStart.Format(time.RFC3339),
		PeriodEnd:       usage.PeriodEnd.Format(time.RFC3339),
		RX:              usage.RX,
		TX:              usage.TX,
		Usage:           usage.Total,
		BandwidthGB:     usage.BandwidthGB,
		PeriodMax:       usage.PeriodMax,
		UsagePercent:    trafficUsagePercent(usage.Total, usage.PeriodMax),
		OverLimit:       usage.OverLimit,
		Daily:           daily,
	}
}

func trafficUsagePercent(used, limit uint64) float64 {
	if limit == 0 {
		return 0
	}
	return float64(used) / float64(limit) * 100
}

func (h *InstanceHandler) instanceNATPortMappings(i *domain.Instance) ([]NATPortMappingResponse, error) {
	if i == nil || i.NetworkMode() != "nat" {
		return nil, nil
	}
	rules, err := h.svc.ListNATPortMappings(i.ID())
	if err != nil {
		return nil, err
	}
	if len(rules) == 0 && i.NATPort() > 0 {
		rules = []contracts.NATForwardRule{{
			HostPort:  i.NATPort(),
			GuestPort: 22,
			Protocol:  "tcp",
		}}
	}
	mappings := make([]NATPortMappingResponse, 0, len(rules))
	for _, rule := range rules {
		if rule.HostPort <= 0 {
			continue
		}
		guestPort := rule.GuestPort
		if guestPort <= 0 {
			guestPort = 22
		}
		protocol := rule.Protocol
		if protocol == "" {
			protocol = "tcp"
		}
		mappings = append(mappings, NATPortMappingResponse{
			HostPort:  rule.HostPort,
			GuestPort: guestPort,
			Protocol:  protocol,
		})
	}
	return mappings, nil
}

func natPortsFromMappings(mappings []NATPortMappingResponse) []int {
	if len(mappings) == 0 {
		return nil
	}
	ports := make([]int, 0, len(mappings))
	for _, mapping := range mappings {
		if mapping.HostPort > 0 {
			ports = append(ports, mapping.HostPort)
		}
	}
	return ports
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
