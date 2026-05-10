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
	svc     *app.ProvisioningAppService
	console ConsoleService
}

type ConsoleService interface {
	ClaimPendingSessions(nodeID string) []contracts.ConsoleSession
	AttachAgent(nodeID string, stream contracts.ConsoleStream) error
}

func NewAgentGRPCServer(svc *app.ProvisioningAppService, console ConsoleService) *AgentGRPCServer {
	return &AgentGRPCServer{svc: svc, console: console}
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
		VMStates:   protoToRuntimeStates(req.GetVmStates()),
	}
	ack, err := s.svc.Heartbeat(hb)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "heartbeat failed: %v", err)
	}
	if s.console != nil {
		ack.ConsoleSessions = s.console.ClaimPendingSessions(nodeID)
	}
	return &agentpb.HeartbeatResponse{
		Ok:              ack.OK,
		Tasks:           tasksToProto(ack.Tasks),
		NatForwards:     natForwardsToProto(ack.NATForwards),
		ConsoleSessions: consoleSessionsToProto(ack.ConsoleSessions),
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
		VMState:    req.GetVmState(),
		FinishedAt: req.GetFinishedAt(),
	}
	if err := s.svc.ReportTaskResult(result); err != nil {
		return nil, status.Errorf(codes.Internal, "report task result failed: %v", err)
	}
	return &agentpb.TaskResultResponse{Ok: true}, nil
}

func (s *AgentGRPCServer) OpenConsole(stream agentpb.AgentService_OpenConsoleServer) error {
	nodeID, ok := NodeIDFromContext(stream.Context())
	if !ok {
		return status.Errorf(codes.Unauthenticated, "missing authenticated node identity")
	}
	if s.console == nil {
		return status.Errorf(codes.Unavailable, "console service unavailable")
	}
	return s.console.AttachAgent(nodeID, &consoleProtoStream{stream: stream})
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

func protoToRuntimeStates(states []*agentpb.VMState) []contracts.InstanceRuntimeState {
	out := make([]contracts.InstanceRuntimeState, 0, len(states))
	for _, state := range states {
		if state == nil || state.GetInstanceId() == "" {
			continue
		}
		out = append(out, contracts.InstanceRuntimeState{
			InstanceID:    state.GetInstanceId(),
			State:         state.GetState(),
			IPv4:          state.GetIpv4(),
			IPv6:          state.GetIpv6(),
			ReportedAt:    state.GetReportedAt(),
			VMTransferred: protoToVMTransferred(state.GetVmTransferred()),
		})
	}
	return out
}

func protoToVMTransferred(src *agentpb.VMTransferred) contracts.VMTransferred {
	if src == nil {
		return contracts.VMTransferred{}
	}
	return contracts.VMTransferred{
		Total: src.GetTotal(),
		RX:    src.GetRx(),
		TX:    src.GetTx(),
	}
}

func specToProto(s contracts.ProvisionSpec) *agentpb.ProvisionSpec {
	return &agentpb.ProvisionSpec{
		InstanceId:      s.InstanceID,
		Hostname:        s.Hostname,
		Os:              s.OS,
		Cpu:             int32(s.CPU),
		MemoryMb:        int32(s.MemoryMB),
		DiskGb:          int32(s.DiskGB),
		Ipv4:            s.IPv4,
		Ipv6:            s.IPv6,
		VirtType:        string(s.VirtType),
		StoragePool:     s.StoragePool,
		NetworkName:     s.NetworkName,
		SshKeys:         s.SSHKeys,
		NetworkMode:     string(s.NetworkMode),
		NatPort:         int32(s.NATPort),
		InitialPassword: s.InitialPassword,
		NatForwards:     natForwardsToProto(s.NATForwards),
	}
}

func natForwardsToProto(rules []contracts.NATForwardRule) []*agentpb.NATForwardRule {
	out := make([]*agentpb.NATForwardRule, len(rules))
	for i, rule := range rules {
		out[i] = &agentpb.NATForwardRule{
			InstanceId: rule.InstanceID,
			HostPort:   int32(rule.HostPort),
			GuestIp:    rule.GuestIP,
			GuestPort:  int32(rule.GuestPort),
			Protocol:   rule.Protocol,
		}
	}
	return out
}

func consoleSessionsToProto(items []contracts.ConsoleSession) []*agentpb.ConsoleSession {
	out := make([]*agentpb.ConsoleSession, 0, len(items))
	for _, item := range items {
		if item.SessionID == "" || item.InstanceID == "" {
			continue
		}
		out = append(out, &agentpb.ConsoleSession{
			SessionId:  item.SessionID,
			InstanceId: item.InstanceID,
		})
	}
	return out
}

type consoleProtoStream struct {
	stream agentpb.AgentService_OpenConsoleServer
}

func (s *consoleProtoStream) Send(frame contracts.ConsoleFrame) error {
	return s.stream.Send(&agentpb.ConsoleFrame{
		SessionId:  frame.SessionID,
		InstanceId: frame.InstanceID,
		Data:       frame.Data,
		Error:      frame.Error,
		Control:    frame.Control,
	})
}

func (s *consoleProtoStream) Recv() (contracts.ConsoleFrame, error) {
	frame, err := s.stream.Recv()
	if err != nil {
		return contracts.ConsoleFrame{}, err
	}
	return contracts.ConsoleFrame{
		SessionID:  frame.GetSessionId(),
		InstanceID: frame.GetInstanceId(),
		Data:       frame.GetData(),
		Error:      frame.GetError(),
		Control:    frame.GetControl(),
	}, nil
}
