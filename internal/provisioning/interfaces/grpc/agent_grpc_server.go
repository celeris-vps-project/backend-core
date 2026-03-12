package grpc

import (
	"backend-core/internal/provisioning/app"
	"backend-core/pkg/agentpb"
	"backend-core/pkg/contracts"
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// AgentGRPCServer implements agentpb.AgentServiceServer and delegates to the
// same ProvisioningAppService used by the HTTP handlers.
type AgentGRPCServer struct {
	agentpb.UnimplementedAgentServiceServer
	svc *app.ProvisioningAppService
}

func NewAgentGRPCServer(svc *app.ProvisioningAppService) *AgentGRPCServer {
	return &AgentGRPCServer{svc: svc}
}

// Register handles agent bootstrap registration.
func (s *AgentGRPCServer) Register(ctx context.Context, req *agentpb.RegisterRequest) (*agentpb.RegisterResponse, error) {
	reg := contracts.AgentRegistration{
		BootstrapToken: req.GetBootstrapToken(),
		Hostname:       req.GetHostname(),
		IP:             req.GetIp(),
		Version:        req.GetVersion(),
	}
	result, err := s.svc.RegisterAgent(reg)
	if err != nil {
		return nil, status.Errorf(codes.Unauthenticated, "registration failed: %v", err)
	}
	return &agentpb.RegisterResponse{Ok: true, NodeId: result.NodeID, NodeToken: result.NodeToken}, nil
}

// Heartbeat handles periodic agent health reports and returns queued tasks.
func (s *AgentGRPCServer) Heartbeat(ctx context.Context, req *agentpb.HeartbeatRequest) (*agentpb.HeartbeatResponse, error) {
	// Use the authenticated node ID from the interceptor (ignore request's node_id)
	nodeID, ok := NodeIDFromContext(ctx)
	if !ok {
		return nil, status.Errorf(codes.Unauthenticated, "missing authenticated node identity")
	}
	hb := contracts.Heartbeat{
		NodeID:     nodeID,
		CPUUsage:   req.GetCpuUsage(),
		MemUsage:   req.GetMemUsage(),
		DiskUsage:  req.GetDiskUsage(),
		Uptime:     req.GetUptime(),
		VMCount:    int(req.GetVmCount()),
		ReportedAt: req.GetReportedAt(),
	}
	ack, err := s.svc.Heartbeat(hb)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "heartbeat failed: %v", err)
	}
	return &agentpb.HeartbeatResponse{
		Ok:    ack.OK,
		Tasks: tasksToProto(ack.Tasks),
	}, nil
}

// ReportTaskResult handles task completion/failure reports from the agent.
func (s *AgentGRPCServer) ReportTaskResult(ctx context.Context, req *agentpb.TaskResultRequest) (*agentpb.TaskResultResponse, error) {
	// Verify the caller is authenticated via the interceptor
	if _, ok := NodeIDFromContext(ctx); !ok {
		return nil, status.Errorf(codes.Unauthenticated, "missing authenticated node identity")
	}
	result := contracts.TaskResult{
		TaskID:     req.GetTaskId(),
		Status:     contracts.TaskStatus(req.GetStatus()),
		Error:      req.GetError(),
		IPv4:       req.GetIpv4(),
		IPv6:       req.GetIpv6(),
		FinishedAt: req.GetFinishedAt(),
	}
	if err := s.svc.ReportTaskResult(result); err != nil {
		return nil, status.Errorf(codes.Internal, "report task result failed: %v", err)
	}
	return &agentpb.TaskResultResponse{Ok: true}, nil
}

// ---- proto ---?contracts mapping helpers ----

func tasksToProto(tasks []contracts.Task) []*agentpb.Task {
	out := make([]*agentpb.Task, len(tasks))
	for i, t := range tasks {
		out[i] = &agentpb.Task{
			Id:         t.ID,
			NodeId:     t.NodeID,
			Type:       string(t.Type),
			Status:     string(t.Status),
			Spec:       specToProto(t.Spec),
			Error:      t.Error,
			CreatedAt:  t.CreatedAt,
			FinishedAt: t.FinishedAt,
		}
	}
	return out
}

func specToProto(s contracts.ProvisionSpec) *agentpb.ProvisionSpec {
	return &agentpb.ProvisionSpec{
		InstanceId:  s.InstanceID,
		Hostname:    s.Hostname,
		Os:          s.OS,
		Cpu:         int32(s.CPU),
		MemoryMb:    int32(s.MemoryMB),
		DiskGb:      int32(s.DiskGB),
		Ipv4:        s.IPv4,
		Ipv6:        s.IPv6,
		VirtType:    string(s.VirtType),
		StoragePool: s.StoragePool,
		NetworkName: s.NetworkName,
		SshKeys:     s.SSHKeys,
	}
}
