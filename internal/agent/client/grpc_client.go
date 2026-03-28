package client

import (
	"backend-core/pkg/agentpb"
	"backend-core/pkg/contracts"
	"context"
	"fmt"
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

// AgentClient wraps the generated gRPC client with methods that accept and
// return the shared contracts types.
type AgentClient struct {
	conn      *grpc.ClientConn
	svc       agentpb.AgentServiceClient
	mu        sync.RWMutex
	nodeToken string // permanent credential, set after registration or loaded from file
}

// Dial creates a new AgentClient connected to the given gRPC address.
func Dial(addr string) (*AgentClient, error) {
	conn, err := grpc.NewClient(addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return nil, fmt.Errorf("grpc dial %s: %w", addr, err)
	}
	return &AgentClient{
		conn: conn,
		svc:  agentpb.NewAgentServiceClient(conn),
	}, nil
}

// Close closes the underlying gRPC connection.
func (c *AgentClient) Close() error {
	return c.conn.Close()
}

// SetNodeToken sets the permanent node credential for authenticating
// subsequent Heartbeat and ReportTaskResult RPCs.
func (c *AgentClient) SetNodeToken(token string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.nodeToken = token
}

// NodeToken returns the current node token.
func (c *AgentClient) NodeToken() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.nodeToken
}

// authCtx attaches the node-token metadata to outgoing gRPC calls.
func (c *AgentClient) authCtx(ctx context.Context) context.Context {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.nodeToken != "" {
		return metadata.AppendToOutgoingContext(ctx, "node-token", c.nodeToken)
	}
	return ctx
}

// Register sends the agent bootstrap registration request to the controller.
// On success it returns the registration result containing the node ID and permanent token.
func (c *AgentClient) Register(ctx context.Context, reg contracts.AgentRegistration) (*contracts.RegistrationResult, error) {
	resp, err := c.svc.Register(ctx, &agentpb.RegisterRequest{
		BootstrapToken: reg.BootstrapToken,
		Hostname:       reg.Hostname,
		Ip:             reg.IP,
		Version:        reg.Version,
	})
	if err != nil {
		return nil, err
	}
	nodeToken := resp.GetNodeToken()
	c.SetNodeToken(nodeToken)
	return &contracts.RegistrationResult{
		NodeID:    resp.GetNodeId(),
		NodeToken: nodeToken,
	}, nil
}

// Heartbeat sends a heartbeat and returns the ack (with optional queued tasks).
// The node-token is automatically attached via gRPC metadata.
func (c *AgentClient) Heartbeat(ctx context.Context, hb contracts.Heartbeat) (*contracts.HeartbeatAck, error) {
	resp, err := c.svc.Heartbeat(c.authCtx(ctx), &agentpb.HeartbeatRequest{
		NodeId:     hb.NodeID,
		CpuUsage:   hb.CPUUsage,
		MemUsage:   hb.MemUsage,
		DiskUsage:  hb.DiskUsage,
		Uptime:     hb.Uptime,
		VmCount:    int32(hb.VMCount),
		ReportedAt: hb.ReportedAt,
	})
	if err != nil {
		return nil, err
	}
	return &contracts.HeartbeatAck{
		OK:    resp.GetOk(),
		Tasks: protoToTasks(resp.GetTasks()),
	}, nil
}

// ReportTaskResult sends a task result back to the controller.
// The node-token is automatically attached via gRPC metadata.
func (c *AgentClient) ReportTaskResult(ctx context.Context, result contracts.TaskResult) error {
	_, err := c.svc.ReportTaskResult(c.authCtx(ctx), &agentpb.TaskResultRequest{
		TaskId:     result.TaskID,
		Status:     string(result.Status),
		Error:      result.Error,
		Ipv4:       result.IPv4,
		Ipv6:       result.IPv6,
		FinishedAt: result.FinishedAt,
		VmState:    result.VMState,
	})
	return err
}

// ---- proto �?contracts mapping helpers ----

func protoToTasks(pts []*agentpb.Task) []contracts.Task {
	out := make([]contracts.Task, len(pts))
	for i, pt := range pts {
		out[i] = contracts.Task{
			ID:         pt.GetId(),
			NodeID:     pt.GetNodeId(),
			Type:       contracts.TaskType(pt.GetType()),
			Status:     contracts.TaskStatus(pt.GetStatus()),
			Spec:       protoToSpec(pt.GetSpec()),
			Error:      pt.GetError(),
			CreatedAt:  pt.GetCreatedAt(),
			FinishedAt: pt.GetFinishedAt(),
		}
	}
	return out
}

func protoToSpec(ps *agentpb.ProvisionSpec) contracts.ProvisionSpec {
	if ps == nil {
		return contracts.ProvisionSpec{}
	}
	return contracts.ProvisionSpec{
		InstanceID:  ps.GetInstanceId(),
		Hostname:    ps.GetHostname(),
		OS:          ps.GetOs(),
		CPU:         int(ps.GetCpu()),
		MemoryMB:    int(ps.GetMemoryMb()),
		DiskGB:      int(ps.GetDiskGb()),
		IPv4:        ps.GetIpv4(),
		IPv6:        ps.GetIpv6(),
		VirtType:    contracts.VirtType(ps.GetVirtType()),
		StoragePool: ps.GetStoragePool(),
		NetworkName: ps.GetNetworkName(),
		SSHKeys:     ps.GetSshKeys(),
	}
}
