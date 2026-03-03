package client

import (
	"backend-core/pkg/agentpb"
	"backend-core/pkg/contracts"
	"context"
	"fmt"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// AgentClient wraps the generated gRPC client with methods that accept and
// return the shared contracts types.
type AgentClient struct {
	conn *grpc.ClientConn
	svc  agentpb.AgentServiceClient
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

// Register sends the agent registration request to the controller.
func (c *AgentClient) Register(ctx context.Context, reg contracts.AgentRegistration) error {
	_, err := c.svc.Register(ctx, &agentpb.RegisterRequest{
		NodeId:   reg.NodeID,
		Secret:   reg.Secret,
		Hostname: reg.Hostname,
		Location: reg.Location,
		Ip:       reg.IP,
		Version:  reg.Version,
	})
	return err
}

// Heartbeat sends a heartbeat and returns the ack (with optional queued tasks).
func (c *AgentClient) Heartbeat(ctx context.Context, hb contracts.Heartbeat) (*contracts.HeartbeatAck, error) {
	resp, err := c.svc.Heartbeat(ctx, &agentpb.HeartbeatRequest{
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
func (c *AgentClient) ReportTaskResult(ctx context.Context, result contracts.TaskResult) error {
	_, err := c.svc.ReportTaskResult(ctx, &agentpb.TaskResultRequest{
		TaskId:     result.TaskID,
		Status:     string(result.Status),
		Error:      result.Error,
		Ipv4:       result.IPv4,
		Ipv6:       result.IPv6,
		FinishedAt: result.FinishedAt,
	})
	return err
}

// ---- proto → contracts mapping helpers ----

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
