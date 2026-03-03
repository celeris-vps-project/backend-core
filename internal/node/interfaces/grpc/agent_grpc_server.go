package grpc

import (
	"backend-core/internal/node/app"
	"backend-core/pkg/agentpb"
	"backend-core/pkg/contracts"
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// AgentGRPCServer implements agentpb.AgentServiceServer and delegates to the
// same NodeAppService used by the HTTP handlers.
type AgentGRPCServer struct {
	agentpb.UnimplementedAgentServiceServer
	svc *app.NodeAppService
}

func NewAgentGRPCServer(svc *app.NodeAppService) *AgentGRPCServer {
	return &AgentGRPCServer{svc: svc}
}

// Register handles agent registration.
func (s *AgentGRPCServer) Register(ctx context.Context, req *agentpb.RegisterRequest) (*agentpb.RegisterResponse, error) {
	reg := contracts.AgentRegistration{
		NodeID:   req.GetNodeId(),
		Secret:   req.GetSecret(),
		Hostname: req.GetHostname(),
		IP:       req.GetIp(),
		Version:  req.GetVersion(),
	}
	if err := s.svc.RegisterAgent(reg); err != nil {
		return nil, status.Errorf(codes.Unauthenticated, "registration failed: %v", err)
	}
	return &agentpb.RegisterResponse{Ok: true}, nil
}

// Heartbeat handles periodic agent health reports and returns queued tasks.
func (s *AgentGRPCServer) Heartbeat(ctx context.Context, req *agentpb.HeartbeatRequest) (*agentpb.HeartbeatResponse, error) {
	hb := contracts.Heartbeat{
		NodeID:     req.GetNodeId(),
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

// ---- proto ↔ contracts mapping helpers ----

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
