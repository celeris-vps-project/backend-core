package app

import (
	"backend-core/internal/node/domain"
	"backend-core/pkg/contracts"
	"errors"
	"time"
)

type IDGenerator interface{ NewID() string }

type NodeAppService struct {
	hostRepo domain.HostNodeRepository
	ipRepo   domain.IPAddressRepository
	taskRepo domain.TaskRepository
	ids      IDGenerator
}

func NewNodeAppService(
	hostRepo domain.HostNodeRepository,
	ipRepo domain.IPAddressRepository,
	taskRepo domain.TaskRepository,
	ids IDGenerator,
) *NodeAppService {
	return &NodeAppService{hostRepo: hostRepo, ipRepo: ipRepo, taskRepo: taskRepo, ids: ids}
}

// ---- Host CRUD ----

func (s *NodeAppService) CreateHost(code, location, name, secret string) (*domain.HostNode, error) {
	id := s.ids.NewID()
	h, err := domain.NewHostNode(id, code, location, name, secret)
	if err != nil {
		return nil, err
	}
	if err := s.hostRepo.Save(h); err != nil {
		return nil, err
	}
	return h, nil
}

func (s *NodeAppService) GetHost(id string) (*domain.HostNode, error) { return s.hostRepo.GetByID(id) }
func (s *NodeAppService) ListHosts() ([]*domain.HostNode, error)      { return s.hostRepo.ListAll() }
func (s *NodeAppService) ListHostsByLocation(loc string) ([]*domain.HostNode, error) {
	return s.hostRepo.ListByLocation(loc)
}

// ---- Agent registration & heartbeat ----

func (s *NodeAppService) RegisterAgent(reg contracts.AgentRegistration) error {
	h, err := s.hostRepo.GetByID(reg.NodeID)
	if err != nil {
		return err
	}
	if !h.ValidateSecret(reg.Secret) {
		return errors.New("app_error: invalid agent secret")
	}
	h.Register(reg.IP, reg.Version, time.Now())
	return s.hostRepo.Save(h)
}

func (s *NodeAppService) Heartbeat(hb contracts.Heartbeat) (*contracts.HeartbeatAck, error) {
	h, err := s.hostRepo.GetByID(hb.NodeID)
	if err != nil {
		return nil, err
	}
	h.RecordHeartbeat(hb.CPUUsage, hb.MemUsage, hb.DiskUsage, hb.VMCount, time.Now())
	if err := s.hostRepo.Save(h); err != nil {
		return nil, err
	}

	// Return any queued tasks for this node
	tasks, err := s.taskRepo.ListPendingByNodeID(hb.NodeID)
	if err != nil {
		return &contracts.HeartbeatAck{OK: true}, nil
	}
	return &contracts.HeartbeatAck{OK: true, Tasks: tasks}, nil
}

// ---- Task result callback ----

func (s *NodeAppService) ReportTaskResult(result contracts.TaskResult) error {
	task, err := s.taskRepo.GetByID(result.TaskID)
	if err != nil {
		return err
	}
	task.Status = result.Status
	task.Error = result.Error
	task.FinishedAt = result.FinishedAt
	return s.taskRepo.Save(task)
}

// ---- Enqueue a task (called by instance domain or internally) ----

func (s *NodeAppService) EnqueueTask(nodeID string, taskType contracts.TaskType, spec contracts.ProvisionSpec) (*contracts.Task, error) {
	task := &contracts.Task{
		ID:        s.ids.NewID(),
		NodeID:    nodeID,
		Type:      taskType,
		Status:    contracts.TaskStatusQueued,
		Spec:      spec,
		CreatedAt: time.Now().Format(time.RFC3339),
	}
	if err := s.taskRepo.Save(task); err != nil {
		return nil, err
	}
	return task, nil
}

// ---- IP management ----

func (s *NodeAppService) AddIP(nodeID, address string, version int) (*domain.IPAddress, error) {
	id := s.ids.NewID()
	ip, err := domain.NewIPAddress(id, nodeID, address, version)
	if err != nil {
		return nil, err
	}
	if err := s.ipRepo.Save(ip); err != nil {
		return nil, err
	}
	return ip, nil
}

func (s *NodeAppService) ListIPs(nodeID string) ([]*domain.IPAddress, error) {
	return s.ipRepo.ListByNodeID(nodeID)
}

func (s *NodeAppService) AllocateIP(nodeID string, version int, instanceID string) (*domain.IPAddress, error) {
	ip, err := s.ipRepo.FindAvailable(nodeID, version)
	if err != nil {
		return nil, err
	}
	if err := ip.Assign(instanceID); err != nil {
		return nil, err
	}
	if err := s.ipRepo.Save(ip); err != nil {
		return nil, err
	}
	return ip, nil
}

func (s *NodeAppService) ReleaseIP(ipID string) error {
	ip, err := s.ipRepo.GetByID(ipID)
	if err != nil {
		return err
	}
	ip.Release()
	return s.ipRepo.Save(ip)
}
