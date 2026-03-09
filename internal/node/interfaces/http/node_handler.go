package http

import (
	"backend-core/internal/node/app"
	"backend-core/internal/node/domain"
	"backend-core/pkg/contracts"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	hz_app "github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/common/utils"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
)

// ---- Request DTOs ----

type CreateHostRequest struct {
	Code             string `json:"code"`                   // optional — auto-generated from location if empty
	Location         string `json:"location" vd:"len($)>0"` // required
	Name             string `json:"name"`                   // optional — falls back to code if empty
	TotalSlots       int    `json:"total_slots"`
	TokenTTLMinutes  int    `json:"token_ttl_minutes"` // TTL for the auto-created bootstrap token (default 24h)
	TokenDescription string `json:"token_description"` // optional description for the bootstrap token
}

type AddIPRequest struct {
	Address string `json:"address" vd:"len($)>0"`
	Version int    `json:"version" vd:"$==4||$==6"`
}

type EnqueueTaskRequest struct {
	Type contracts.TaskType      `json:"type" vd:"len($)>0"`
	Spec contracts.ProvisionSpec `json:"spec"`
}

type CreateResourcePoolRequest struct {
	Name     string `json:"name" vd:"len($)>0"`
	RegionID string `json:"region_id" vd:"len($)>0"`
}

type UpdateResourcePoolRequest struct {
	Name     string `json:"name"`
	RegionID string `json:"region_id"`
}

type AssignNodeRequest struct {
	NodeID string `json:"node_id" vd:"len($)>0"`
}

// ---- Response DTOs ----

type HostNodeResponse struct {
	ID             string  `json:"id"`
	Code           string  `json:"code"`
	Location       string  `json:"location"`
	ResourcePoolID string  `json:"resource_pool_id,omitempty"`
	Name           string  `json:"name"`
	IP             string  `json:"ip,omitempty"`
	Status         string  `json:"status"`
	AgentVer       string  `json:"agent_ver,omitempty"`
	CPUUsage       float64 `json:"cpu_usage"`
	MemUsage       float64 `json:"mem_usage"`
	DiskUsage      float64 `json:"disk_usage"`
	VMCount        int     `json:"vm_count"`
	TotalSlots     int     `json:"total_slots"`
	UsedSlots      int     `json:"used_slots"`
	AvailableSlots int     `json:"available_slots"`
	Enabled        bool    `json:"enabled"`
	LastSeen       *string `json:"last_seen_at,omitempty"`
	CreatedAt      string  `json:"created_at"`
}

type IPResponse struct {
	ID         string `json:"id"`
	NodeID     string `json:"node_id"`
	Address    string `json:"address"`
	Version    int    `json:"version"`
	InstanceID string `json:"instance_id,omitempty"`
	Available  bool   `json:"available"`
}

type ResourcePoolResponse struct {
	ID             string                    `json:"id"`
	Name           string                    `json:"name"`
	RegionID       string                    `json:"region_id"`
	Status         string                    `json:"status"`
	TotalSlots     int                       `json:"total_slots,omitempty"`
	UsedSlots      int                       `json:"used_slots,omitempty"`
	AvailableSlots int                       `json:"available_slots,omitempty"`
	Nodes          []ResourcePoolNodeSummary `json:"nodes,omitempty"`
}

type ResourcePoolNodeSummary struct {
	ID             string `json:"id"`
	Code           string `json:"code"`
	Name           string `json:"name"`
	Status         string `json:"status"`
	TotalSlots     int    `json:"total_slots"`
	UsedSlots      int    `json:"used_slots"`
	AvailableSlots int    `json:"available_slots"`
	Enabled        bool   `json:"enabled"`
}

// ---- Handler ----

type NodeHandler struct{ svc *app.NodeAppService }

func NewNodeHandler(svc *app.NodeAppService) *NodeHandler { return &NodeHandler{svc: svc} }

// ══════════════════════════════════════════════════════════════════════
// Host Node endpoints
// ══════════════════════════════════════════════════════════════════════

// POST /host-nodes
func (h *NodeHandler) CreateHost(ctx context.Context, c *hz_app.RequestContext) {
	var req CreateHostRequest
	if err := c.BindAndValidate(&req); err != nil {
		c.JSON(consts.StatusBadRequest, utils.H{"error": err.Error()})
		return
	}

	// Auto-generate code from location + random suffix if not provided
	code := req.Code
	if code == "" {
		code = fmt.Sprintf("%s-%s", req.Location, shortRandHex(3))
	}

	// If no name provided, fall back to the node code as display label
	name := req.Name
	if name == "" {
		name = code
	}

	// Auto-generate an internal secret (legacy field; agents use bootstrap tokens now)
	autoSecret, err := domain.GenerateNodeToken()
	if err != nil {
		c.JSON(consts.StatusInternalServerError, utils.H{"error": "failed to generate node secret"})
		return
	}

	node, err := h.svc.CreateHost(code, req.Location, name, autoSecret, req.TotalSlots)
	if err != nil {
		c.JSON(consts.StatusUnprocessableEntity, utils.H{"error": err.Error()})
		return
	}

	// Auto-create a bootstrap token for the new node
	ttl := time.Duration(req.TokenTTLMinutes) * time.Minute
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	desc := req.TokenDescription
	if desc == "" {
		desc = "Auto-created for node " + code
	}
	bt, err := h.svc.CreateBootstrapToken(node.ID(), ttl, desc)
	if err != nil {
		// Node was created but token creation failed — still return the node
		c.JSON(consts.StatusCreated, utils.H{
			"data":  toHostResp(node, nil),
			"error": "node created but bootstrap token generation failed: " + err.Error(),
		})
		return
	}

	c.JSON(consts.StatusCreated, utils.H{
		"data":            toHostResp(node, nil),
		"bootstrap_token": toBtResp(bt),
	})
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
	// Fetch all cached states in one call
	allStates, _ := h.svc.StateCache().GetAllNodeStates()
	list := make([]HostNodeResponse, len(nodes))
	for i, n := range nodes {
		list[i] = toHostResp(n, allStates[n.ID()])
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
	state, _ := h.svc.StateCache().GetNodeState(node.ID())
	c.JSON(consts.StatusOK, utils.H{"data": toHostResp(node, state)})
}

// POST /host-nodes/:id/enable
func (h *NodeHandler) EnableHost(ctx context.Context, c *hz_app.RequestContext) {
	if err := h.svc.EnableHost(c.Param("id")); err != nil {
		c.JSON(consts.StatusUnprocessableEntity, utils.H{"error": err.Error()})
		return
	}
	node, _ := h.svc.GetHost(c.Param("id"))
	state, _ := h.svc.StateCache().GetNodeState(node.ID())
	c.JSON(consts.StatusOK, utils.H{"data": toHostResp(node, state)})
}

// POST /host-nodes/:id/disable
func (h *NodeHandler) DisableHost(ctx context.Context, c *hz_app.RequestContext) {
	if err := h.svc.DisableHost(c.Param("id")); err != nil {
		c.JSON(consts.StatusUnprocessableEntity, utils.H{"error": err.Error()})
		return
	}
	node, _ := h.svc.GetHost(c.Param("id"))
	state, _ := h.svc.StateCache().GetNodeState(node.ID())
	c.JSON(consts.StatusOK, utils.H{"data": toHostResp(node, state)})
}

// GET /locations
func (h *NodeHandler) ListLocations(ctx context.Context, c *hz_app.RequestContext) {
	locs, err := h.svc.AvailableLocations()
	if err != nil {
		c.JSON(consts.StatusInternalServerError, utils.H{"error": err.Error()})
		return
	}
	c.JSON(consts.StatusOK, utils.H{"data": locs})
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

// POST /host-nodes/:id/tasks
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

// ══════════════════════════════════════════════════════════════════════
// Agent endpoints (called by cmd/agent)
// ══════════════════════════════════════════════════════════════════════

// POST /agent/register
func (h *NodeHandler) AgentRegister(ctx context.Context, c *hz_app.RequestContext) {
	var reg contracts.AgentRegistration
	if err := c.BindAndValidate(&reg); err != nil {
		c.JSON(consts.StatusBadRequest, utils.H{"error": err.Error()})
		return
	}
	result, err := h.svc.RegisterAgent(reg)
	if err != nil {
		c.JSON(consts.StatusUnauthorized, utils.H{"error": err.Error()})
		return
	}
	c.JSON(consts.StatusOK, utils.H{"ok": true, "node_id": result.NodeID, "node_token": result.NodeToken})
}

// ══════════════════════════════════════════════════════════════════════
// Bootstrap Token Management (Admin)
// ══════════════════════════════════════════════════════════════════════

type CreateBootstrapTokenRequest struct {
	NodeID      string `json:"node_id" vd:"len($)>0"`
	TTLMinutes  int    `json:"ttl_minutes"`
	Description string `json:"description"`
}

type BootstrapTokenResponse struct {
	ID           string  `json:"id"`
	NodeID       string  `json:"node_id"`
	Token        string  `json:"token"`
	ExpiresAt    string  `json:"expires_at"`
	Used         bool    `json:"used"`
	UsedByNodeID string  `json:"used_by_node_id,omitempty"`
	UsedAt       *string `json:"used_at,omitempty"`
	CreatedAt    string  `json:"created_at"`
	Description  string  `json:"description,omitempty"`
}

// POST /admin/bootstrap-tokens
func (h *NodeHandler) CreateBootstrapToken(ctx context.Context, c *hz_app.RequestContext) {
	var req CreateBootstrapTokenRequest
	if err := c.BindAndValidate(&req); err != nil {
		c.JSON(consts.StatusBadRequest, utils.H{"error": err.Error()})
		return
	}
	ttl := time.Duration(req.TTLMinutes) * time.Minute
	if ttl <= 0 {
		ttl = 24 * time.Hour // default 24h
	}
	bt, err := h.svc.CreateBootstrapToken(req.NodeID, ttl, req.Description)
	if err != nil {
		c.JSON(consts.StatusUnprocessableEntity, utils.H{"error": err.Error()})
		return
	}
	c.JSON(consts.StatusCreated, utils.H{"data": toBtResp(bt)})
}

// GET /admin/bootstrap-tokens
func (h *NodeHandler) ListBootstrapTokens(ctx context.Context, c *hz_app.RequestContext) {
	tokens, err := h.svc.ListBootstrapTokens()
	if err != nil {
		c.JSON(consts.StatusInternalServerError, utils.H{"error": err.Error()})
		return
	}
	list := make([]BootstrapTokenResponse, len(tokens))
	for i, bt := range tokens {
		list[i] = toBtResp(bt)
	}
	c.JSON(consts.StatusOK, utils.H{"data": list})
}

// DELETE /admin/bootstrap-tokens/:id
func (h *NodeHandler) RevokeBootstrapToken(ctx context.Context, c *hz_app.RequestContext) {
	if err := h.svc.RevokeBootstrapToken(c.Param("id")); err != nil {
		c.JSON(consts.StatusUnprocessableEntity, utils.H{"error": err.Error()})
		return
	}
	c.JSON(consts.StatusOK, utils.H{"ok": true})
}

// POST /admin/nodes/:id/revoke-token
func (h *NodeHandler) RevokeNodeToken(ctx context.Context, c *hz_app.RequestContext) {
	if err := h.svc.RevokeNodeToken(c.Param("id")); err != nil {
		c.JSON(consts.StatusUnprocessableEntity, utils.H{"error": err.Error()})
		return
	}
	c.JSON(consts.StatusOK, utils.H{"ok": true})
}

func toBtResp(bt *domain.BootstrapToken) BootstrapTokenResponse {
	resp := BootstrapTokenResponse{
		ID:           bt.ID(),
		NodeID:       bt.NodeID(),
		Token:        bt.Token(),
		ExpiresAt:    bt.ExpiresAt().Format(time.RFC3339),
		Used:         bt.Used(),
		UsedByNodeID: bt.UsedByNodeID(),
		CreatedAt:    bt.CreatedAt().Format(time.RFC3339),
		Description:  bt.Description(),
	}
	if bt.UsedAt() != nil {
		s := bt.UsedAt().Format(time.RFC3339)
		resp.UsedAt = &s
	}
	return resp
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

// ══════════════════════════════════════════════════════════════════════
// Resource Pool endpoints
// ══════════════════════════════════════════════════════════════════════

// POST /resource-pools
func (h *NodeHandler) CreateResourcePool(ctx context.Context, c *hz_app.RequestContext) {
	var req CreateResourcePoolRequest
	if err := c.BindAndValidate(&req); err != nil {
		c.JSON(consts.StatusBadRequest, utils.H{"error": err.Error()})
		return
	}
	pool, err := h.svc.CreateResourcePool(req.Name, req.RegionID)
	if err != nil {
		c.JSON(consts.StatusUnprocessableEntity, utils.H{"error": err.Error()})
		return
	}
	c.JSON(consts.StatusCreated, utils.H{"data": toPoolResp(pool)})
}

// GET /resource-pools — list all pools with capacity summaries
func (h *NodeHandler) ListResourcePools(ctx context.Context, c *hz_app.RequestContext) {
	summaries, err := h.svc.ListPoolCapacities()
	if err != nil {
		c.JSON(consts.StatusInternalServerError, utils.H{"error": err.Error()})
		return
	}
	c.JSON(consts.StatusOK, utils.H{"data": summaries})
}

// GET /resource-pools/:id — get a single resource pool with nodes
func (h *NodeHandler) GetResourcePool(ctx context.Context, c *hz_app.RequestContext) {
	pool, err := h.svc.GetResourcePool(c.Param("id"))
	if err != nil {
		c.JSON(consts.StatusNotFound, utils.H{"error": err.Error()})
		return
	}
	c.JSON(consts.StatusOK, utils.H{"data": h.toPoolDetailResp(pool)})
}

// PUT /resource-pools/:id
func (h *NodeHandler) UpdateResourcePool(ctx context.Context, c *hz_app.RequestContext) {
	var req UpdateResourcePoolRequest
	if err := c.BindAndValidate(&req); err != nil {
		c.JSON(consts.StatusBadRequest, utils.H{"error": err.Error()})
		return
	}
	pool, err := h.svc.UpdateResourcePool(c.Param("id"), req.Name, req.RegionID)
	if err != nil {
		c.JSON(consts.StatusUnprocessableEntity, utils.H{"error": err.Error()})
		return
	}
	c.JSON(consts.StatusOK, utils.H{"data": toPoolResp(pool)})
}

// POST /resource-pools/:id/activate
func (h *NodeHandler) ActivateResourcePool(ctx context.Context, c *hz_app.RequestContext) {
	if err := h.svc.ActivateResourcePool(c.Param("id")); err != nil {
		c.JSON(consts.StatusUnprocessableEntity, utils.H{"error": err.Error()})
		return
	}
	pool, _ := h.svc.GetResourcePool(c.Param("id"))
	c.JSON(consts.StatusOK, utils.H{"data": toPoolResp(pool)})
}

// POST /resource-pools/:id/deactivate
func (h *NodeHandler) DeactivateResourcePool(ctx context.Context, c *hz_app.RequestContext) {
	if err := h.svc.DeactivateResourcePool(c.Param("id")); err != nil {
		c.JSON(consts.StatusUnprocessableEntity, utils.H{"error": err.Error()})
		return
	}
	pool, _ := h.svc.GetResourcePool(c.Param("id"))
	c.JSON(consts.StatusOK, utils.H{"data": toPoolResp(pool)})
}

// POST /resource-pools/:id/nodes — assign a node to this pool
func (h *NodeHandler) AssignNodeToPool(ctx context.Context, c *hz_app.RequestContext) {
	var req AssignNodeRequest
	if err := c.BindAndValidate(&req); err != nil {
		c.JSON(consts.StatusBadRequest, utils.H{"error": err.Error()})
		return
	}
	if err := h.svc.AssignNodeToPool(req.NodeID, c.Param("id")); err != nil {
		c.JSON(consts.StatusUnprocessableEntity, utils.H{"error": err.Error()})
		return
	}
	c.JSON(consts.StatusOK, utils.H{"ok": true})
}

// DELETE /resource-pools/:id/nodes/:nodeId — remove a node from this pool
func (h *NodeHandler) RemoveNodeFromPool(ctx context.Context, c *hz_app.RequestContext) {
	if err := h.svc.RemoveNodeFromPool(c.Param("nodeId")); err != nil {
		c.JSON(consts.StatusUnprocessableEntity, utils.H{"error": err.Error()})
		return
	}
	c.JSON(consts.StatusOK, utils.H{"ok": true})
}

// ---- Mapping helpers ----

// toHostResp merges persistent config data (from DB) with runtime state (from cache).
// If state is nil, the node is considered offline.
func toHostResp(n *domain.HostNode, state *domain.NodeState) HostNodeResponse {
	resp := HostNodeResponse{
		ID: n.ID(), Code: n.Code(), Location: n.Location(),
		ResourcePoolID: n.ResourcePoolID(), Name: n.Name(),
		Status:     domain.HostStatusOffline,
		CreatedAt:  n.CreatedAt().Format(time.RFC3339),
		TotalSlots: n.TotalSlots(), UsedSlots: n.UsedSlots(),
		AvailableSlots: n.AvailableSlots(), Enabled: n.Enabled(),
	}
	if state != nil {
		resp.Status = state.Status
		resp.IP = state.IP
		resp.AgentVer = state.AgentVer
		resp.CPUUsage = state.CPUUsage
		resp.MemUsage = state.MemUsage
		resp.DiskUsage = state.DiskUsage
		resp.VMCount = state.VMCount
		s := state.LastSeenAt.Format(time.RFC3339)
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

func toPoolResp(p *domain.ResourcePool) ResourcePoolResponse {
	return ResourcePoolResponse{
		ID:       p.ID(),
		Name:     p.Name(),
		RegionID: p.RegionID(),
		Status:   p.Status(),
	}
}

// toPoolDetailResp needs the handler's state cache to show per-node status.
func (h *NodeHandler) toPoolDetailResp(p *domain.ResourcePool) ResourcePoolResponse {
	resp := toPoolResp(p)
	resp.TotalSlots = p.TotalPhysicalSlots()
	resp.UsedSlots = p.UsedPhysicalSlots()
	resp.AvailableSlots = p.AvailablePhysicalSlots()

	allStates, _ := h.svc.StateCache().GetAllNodeStates()

	nodes := make([]ResourcePoolNodeSummary, len(p.Nodes()))
	for i, n := range p.Nodes() {
		status := domain.HostStatusOffline
		if st, ok := allStates[n.ID()]; ok {
			status = st.Status
		}
		nodes[i] = ResourcePoolNodeSummary{
			ID: n.ID(), Code: n.Code(), Name: n.Name(),
			Status: status, TotalSlots: n.TotalSlots(),
			UsedSlots: n.UsedSlots(), AvailableSlots: n.AvailableSlots(),
			Enabled: n.Enabled(),
		}
	}
	resp.Nodes = nodes
	return resp
}

// shortRandHex returns a random hex string of n bytes (2n hex chars).
// Used to generate unique node code suffixes like "a1b2c3".
func shortRandHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
