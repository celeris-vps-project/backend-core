package http

import (
	"backend-core/internal/node/app"
	"backend-core/internal/node/domain"
	"backend-core/pkg/contracts"
	"context"
	"time"

	hz_app "github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/common/utils"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
)

// ---- Request DTOs ----

type CreateHostRequest struct {
	Code     string `json:"code" vd:"len($)>0"`
	Location string `json:"location" vd:"len($)>0"`
	Name     string `json:"name" vd:"len($)>0"`
	Secret   string `json:"secret" vd:"len($)>0"`
}

type AddIPRequest struct {
	Address string `json:"address" vd:"len($)>0"`
	Version int    `json:"version" vd:"$==4||$==6"`
}

type EnqueueTaskRequest struct {
	Type contracts.TaskType      `json:"type" vd:"len($)>0"`
	Spec contracts.ProvisionSpec `json:"spec"`
}

// ---- Response DTOs ----

type HostNodeResponse struct {
	ID        string  `json:"id"`
	Code      string  `json:"code"`
	Location  string  `json:"location"`
	Name      string  `json:"name"`
	IP        string  `json:"ip,omitempty"`
	Status    string  `json:"status"`
	AgentVer  string  `json:"agent_ver,omitempty"`
	CPUUsage  float64 `json:"cpu_usage"`
	MemUsage  float64 `json:"mem_usage"`
	DiskUsage float64 `json:"disk_usage"`
	VMCount   int     `json:"vm_count"`
	LastSeen  *string `json:"last_seen_at,omitempty"`
	CreatedAt string  `json:"created_at"`
}

type IPResponse struct {
	ID         string `json:"id"`
	NodeID     string `json:"node_id"`
	Address    string `json:"address"`
	Version    int    `json:"version"`
	InstanceID string `json:"instance_id,omitempty"`
	Available  bool   `json:"available"`
}

// ---- Handler ----

type NodeHandler struct{ svc *app.NodeAppService }

func NewNodeHandler(svc *app.NodeAppService) *NodeHandler { return &NodeHandler{svc: svc} }

// POST /host-nodes
func (h *NodeHandler) CreateHost(ctx context.Context, c *hz_app.RequestContext) {
	var req CreateHostRequest
	if err := c.BindAndValidate(&req); err != nil {
		c.JSON(consts.StatusBadRequest, utils.H{"error": err.Error()})
		return
	}
	node, err := h.svc.CreateHost(req.Code, req.Location, req.Name, req.Secret)
	if err != nil {
		c.JSON(consts.StatusUnprocessableEntity, utils.H{"error": err.Error()})
		return
	}
	c.JSON(consts.StatusCreated, utils.H{"data": toHostResp(node)})
}

// GET /host-nodes
func (h *NodeHandler) ListHosts(ctx context.Context, c *hz_app.RequestContext) {
	loc := c.Query("location")
	var nodes []*domain.HostNode
	var err error
	if loc != "" {
		nodes, err = h.svc.ListHostsByLocation(loc)
	} else {
		nodes, err = h.svc.ListHosts()
	}
	if err != nil {
		c.JSON(consts.StatusInternalServerError, utils.H{"error": err.Error()})
		return
	}
	list := make([]HostNodeResponse, len(nodes))
	for i, n := range nodes {
		list[i] = toHostResp(n)
	}
	c.JSON(consts.StatusOK, utils.H{"data": list})
}

// GET /host-nodes/:id
func (h *NodeHandler) GetHost(ctx context.Context, c *hz_app.RequestContext) {
	node, err := h.svc.GetHost(c.Param("id"))
	if err != nil {
		c.JSON(consts.StatusNotFound, utils.H{"error": err.Error()})
		return
	}
	c.JSON(consts.StatusOK, utils.H{"data": toHostResp(node)})
}

// POST /host-nodes/:id/ips
func (h *NodeHandler) AddIP(ctx context.Context, c *hz_app.RequestContext) {
	var req AddIPRequest
	if err := c.BindAndValidate(&req); err != nil {
		c.JSON(consts.StatusBadRequest, utils.H{"error": err.Error()})
		return
	}
	ip, err := h.svc.AddIP(c.Param("id"), req.Address, req.Version)
	if err != nil {
		c.JSON(consts.StatusUnprocessableEntity, utils.H{"error": err.Error()})
		return
	}
	c.JSON(consts.StatusCreated, utils.H{"data": toIPResp(ip)})
}

// GET /host-nodes/:id/ips
func (h *NodeHandler) ListIPs(ctx context.Context, c *hz_app.RequestContext) {
	ips, err := h.svc.ListIPs(c.Param("id"))
	if err != nil {
		c.JSON(consts.StatusInternalServerError, utils.H{"error": err.Error()})
		return
	}
	list := make([]IPResponse, len(ips))
	for i, ip := range ips {
		list[i] = toIPResp(ip)
	}
	c.JSON(consts.StatusOK, utils.H{"data": list})
}

// POST /host-nodes/:id/tasks  — enqueue a provisioning task
func (h *NodeHandler) EnqueueTask(ctx context.Context, c *hz_app.RequestContext) {
	var req EnqueueTaskRequest
	if err := c.BindAndValidate(&req); err != nil {
		c.JSON(consts.StatusBadRequest, utils.H{"error": err.Error()})
		return
	}
	task, err := h.svc.EnqueueTask(c.Param("id"), req.Type, req.Spec)
	if err != nil {
		c.JSON(consts.StatusUnprocessableEntity, utils.H{"error": err.Error()})
		return
	}
	c.JSON(consts.StatusCreated, utils.H{"data": task})
}

// ---- Agent endpoints (called by cmd/agent) ----

// POST /agent/register
func (h *NodeHandler) AgentRegister(ctx context.Context, c *hz_app.RequestContext) {
	var reg contracts.AgentRegistration
	if err := c.BindAndValidate(&reg); err != nil {
		c.JSON(consts.StatusBadRequest, utils.H{"error": err.Error()})
		return
	}
	if err := h.svc.RegisterAgent(reg); err != nil {
		c.JSON(consts.StatusUnauthorized, utils.H{"error": err.Error()})
		return
	}
	c.JSON(consts.StatusOK, utils.H{"ok": true})
}

// POST /agent/heartbeat
func (h *NodeHandler) AgentHeartbeat(ctx context.Context, c *hz_app.RequestContext) {
	var hb contracts.Heartbeat
	if err := c.BindAndValidate(&hb); err != nil {
		c.JSON(consts.StatusBadRequest, utils.H{"error": err.Error()})
		return
	}
	ack, err := h.svc.Heartbeat(hb)
	if err != nil {
		c.JSON(consts.StatusInternalServerError, utils.H{"error": err.Error()})
		return
	}
	c.JSON(consts.StatusOK, ack)
}

// POST /agent/tasks/result
func (h *NodeHandler) AgentTaskResult(ctx context.Context, c *hz_app.RequestContext) {
	var result contracts.TaskResult
	if err := c.BindAndValidate(&result); err != nil {
		c.JSON(consts.StatusBadRequest, utils.H{"error": err.Error()})
		return
	}
	if err := h.svc.ReportTaskResult(result); err != nil {
		c.JSON(consts.StatusUnprocessableEntity, utils.H{"error": err.Error()})
		return
	}
	c.JSON(consts.StatusOK, utils.H{"ok": true})
}

// ---- Mapping ----

func toHostResp(n *domain.HostNode) HostNodeResponse {
	resp := HostNodeResponse{
		ID: n.ID(), Code: n.Code(), Location: n.Location(), Name: n.Name(),
		IP: n.IP(), Status: n.Status(), AgentVer: n.AgentVer(),
		CPUUsage: n.CPUUsage(), MemUsage: n.MemUsage(), DiskUsage: n.DiskUsage(),
		VMCount: n.VMCount(), CreatedAt: n.CreatedAt().Format(time.RFC3339),
	}
	if t := n.LastSeenAt(); t != nil {
		s := t.Format(time.RFC3339)
		resp.LastSeen = &s
	}
	return resp
}

func toIPResp(ip *domain.IPAddress) IPResponse {
	return IPResponse{
		ID: ip.ID(), NodeID: ip.NodeID(), Address: ip.Address(),
		Version: ip.Version(), InstanceID: ip.InstanceID(), Available: ip.IsAvailable(),
	}
}
