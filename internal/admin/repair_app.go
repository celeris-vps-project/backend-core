package admin

import (
	catalogApp "backend-core/internal/catalog/app"
	instanceDomain "backend-core/internal/instance/domain"
	orderDomain "backend-core/internal/ordering/domain"
	provisioningApp "backend-core/internal/provisioning/app"
	"backend-core/pkg/contracts"
	"context"
	"errors"
	"strings"
	"time"
)

type OrderStore interface {
	GetByID(id string) (*orderDomain.Order, error)
	ListAll() ([]*orderDomain.Order, error)
}

type InstanceStore interface {
	GetByID(id string) (*instanceDomain.Instance, error)
	GetByOrderID(orderID string) (*instanceDomain.Instance, error)
	ListAll() ([]*instanceDomain.Instance, error)
}

type ProductReader interface {
	GetProvisionSnapshot(ctx context.Context, id string) (catalogApp.ProductProvisionSnapshot, error)
}

type ProvisioningReader interface {
	AvailableSlotsInResourcePool(poolID string) (int, error)
}

type TaskReader interface {
	ListActiveByInstanceID(instanceID string) ([]contracts.Task, error)
}

type ProvisionDispatcher interface {
	Dispatch(cmd provisioningApp.ProvisionCommand) (*provisioningApp.ProvisionResult, error)
}

type RepairService struct {
	orders       OrderStore
	instances    InstanceStore
	products     ProductReader
	provisioning ProvisioningReader
	tasks        TaskReader
	dispatcher   ProvisionDispatcher
}

type RepairCandidate struct {
	OrderID        string `json:"order_id"`
	InstanceID     string `json:"instance_id"`
	CustomerID     string `json:"customer_id"`
	ProductID      string `json:"product_id"`
	InvoiceID      string `json:"invoice_id"`
	OrderStatus    string `json:"order_status"`
	InstanceStatus string `json:"instance_status"`
	Hostname       string `json:"hostname"`
	OS             string `json:"os"`
	Plan           string `json:"plan"`
	NetworkMode    string `json:"network_mode"`
	ResourcePoolID string `json:"resource_pool_id"`
	AvailableSlots int    `json:"available_slots"`
	CanRepair      bool   `json:"can_repair"`
	Reason         string `json:"reason,omitempty"`
	CreatedAt      string `json:"created_at"`
}

type RepairResult struct {
	Candidate RepairCandidate `json:"candidate"`
	TaskID    string          `json:"task_id,omitempty"`
	NodeID    string          `json:"node_id,omitempty"`
	Queued    bool            `json:"queued"`
}

func NewRepairService(
	orders OrderStore,
	instances InstanceStore,
	products ProductReader,
	provisioning ProvisioningReader,
	tasks TaskReader,
	dispatcher ProvisionDispatcher,
) *RepairService {
	return &RepairService{
		orders: orders, instances: instances, products: products,
		provisioning: provisioning, tasks: tasks, dispatcher: dispatcher,
	}
}

func (s *RepairService) ListProvisioningRepairs(ctx context.Context) ([]RepairCandidate, error) {
	instances, err := s.instances.ListAll()
	if err != nil {
		return nil, err
	}
	out := make([]RepairCandidate, 0)
	for _, inst := range instances {
		if inst == nil || inst.ControlStatus() != instanceDomain.InstanceControlStatusProvisioning {
			continue
		}
		candidate, err := s.buildCandidate(ctx, inst.OrderID(), inst)
		if err != nil {
			continue
		}
		if candidate.OrderStatus == orderDomain.OrderStatusActive {
			out = append(out, candidate)
		}
	}
	return out, nil
}

func (s *RepairService) RepairProvisioning(ctx context.Context, orderID string) (*RepairResult, error) {
	orderID = strings.TrimSpace(orderID)
	if orderID == "" {
		return nil, errors.New("domain_error: order id is required")
	}
	inst, err := s.instances.GetByOrderID(orderID)
	if err != nil {
		return nil, err
	}
	candidate, err := s.buildCandidate(ctx, orderID, inst)
	if err != nil {
		return nil, err
	}
	if !candidate.CanRepair {
		return nil, errors.New(candidate.Reason)
	}
	order, err := s.orders.GetByID(orderID)
	if err != nil {
		return nil, err
	}
	product, err := s.products.GetProvisionSnapshot(ctx, order.ProductID())
	if err != nil {
		return nil, err
	}
	cfg := order.VPSConfig()
	result, err := s.dispatcher.Dispatch(provisioningApp.ProvisionCommand{
		InstanceID:      inst.ID(),
		ProductID:       order.ProductID(),
		ProductSlug:     product.Slug,
		ProductType:     "vps",
		ResourcePoolID:  product.ResourcePoolID,
		RegionID:        product.RegionID,
		CustomerID:      order.CustomerID(),
		OrderID:         order.ID(),
		Hostname:        inst.Hostname(),
		OS:              inst.OS(),
		CPU:             cfg.CPU(),
		MemoryMB:        cfg.MemoryMB(),
		DiskGB:          cfg.DiskGB(),
		InitialPassword: inst.InitialPassword(),
		NetworkMode:     repairNetworkMode(cfg.NetworkMode(), product.NetworkMode),
		NATPortCount:    product.NATPortCount,
	})
	if err != nil {
		return nil, err
	}
	if result == nil || !result.Success {
		candidate.CanRepair = false
		candidate.Reason = "domain_error: provisioning dispatch did not queue a task"
		return &RepairResult{Candidate: candidate, Queued: false}, nil
	}
	return &RepairResult{
		Candidate: candidate,
		TaskID:    result.TaskID,
		NodeID:    result.NodeID,
		Queued:    true,
	}, nil
}

func (s *RepairService) buildCandidate(ctx context.Context, orderID string, inst *instanceDomain.Instance) (RepairCandidate, error) {
	order, err := s.orders.GetByID(orderID)
	if err != nil {
		return RepairCandidate{}, err
	}
	product, err := s.products.GetProvisionSnapshot(ctx, order.ProductID())
	if err != nil {
		return RepairCandidate{}, err
	}
	availableSlots := 0
	if product.ResourcePoolID != "" {
		availableSlots, _ = s.provisioning.AvailableSlotsInResourcePool(product.ResourcePoolID)
	}
	c := RepairCandidate{
		OrderID:        order.ID(),
		InstanceID:     inst.ID(),
		CustomerID:     order.CustomerID(),
		ProductID:      order.ProductID(),
		InvoiceID:      order.InvoiceID(),
		OrderStatus:    order.Status(),
		InstanceStatus: inst.ControlStatus(),
		Hostname:       inst.Hostname(),
		OS:             inst.OS(),
		Plan:           inst.Plan(),
		NetworkMode:    inst.NetworkMode(),
		ResourcePoolID: product.ResourcePoolID,
		AvailableSlots: availableSlots,
		CreatedAt:      inst.CreatedAt().Format(time.RFC3339),
	}
	c.CanRepair, c.Reason = s.canRepair(c, product, inst)
	return c, nil
}

func (s *RepairService) canRepair(c RepairCandidate, product catalogApp.ProductProvisionSnapshot, inst *instanceDomain.Instance) (bool, string) {
	if c.OrderStatus != orderDomain.OrderStatusActive {
		return false, "domain_error: only active paid orders can be repaired"
	}
	if c.InstanceStatus != instanceDomain.InstanceControlStatusProvisioning {
		return false, "domain_error: only provisioning instances can be repaired"
	}
	if product.ResourcePoolID == "" {
		return false, "domain_error: product has no resource pool"
	}
	if c.AvailableSlots <= 0 {
		return false, "domain_error: no available physical slots in resource pool"
	}
	if active, err := s.tasks.ListActiveByInstanceID(inst.ID()); err == nil && len(active) > 0 {
		return false, "domain_error: an active provisioning task already exists"
	}
	return true, ""
}

func repairNetworkMode(orderMode, productMode string) string {
	orderMode = strings.TrimSpace(orderMode)
	if orderMode != "" {
		return orderMode
	}
	return productMode
}
