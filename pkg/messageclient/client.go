package messageclient

import (
	"backend-core/pkg/messagepb"
	"context"
	"fmt"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

const ServiceTokenMetadataKey = "x-service-token"

type Config struct {
	Address      string
	ServiceToken string
	Timeout      time.Duration
}

type Client struct {
	conn         *grpc.ClientConn
	svc          messagepb.MessageServiceClient
	serviceToken string
	timeout      time.Duration
}

func Dial(cfg Config) (*Client, error) {
	address := strings.TrimSpace(cfg.Address)
	if address == "" {
		return nil, fmt.Errorf("message address must not be blank")
	}

	conn, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("grpc dial %s: %w", address, err)
	}

	client, err := newClient(conn, cfg.ServiceToken, cfg.Timeout)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}

	return client, nil
}

func newClient(conn *grpc.ClientConn, serviceToken string, timeout time.Duration) (*Client, error) {
	if conn == nil {
		return nil, fmt.Errorf("grpc connection must not be nil")
	}
	serviceToken = strings.TrimSpace(serviceToken)
	if serviceToken == "" {
		return nil, fmt.Errorf("message service token must not be blank")
	}
	if timeout <= 0 {
		timeout = 2 * time.Second
	}

	return &Client{
		conn:         conn,
		svc:          messagepb.NewMessageServiceClient(conn),
		serviceToken: serviceToken,
		timeout:      timeout,
	}, nil
}

func (c *Client) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

func (c *Client) Send(ctx context.Context, req *messagepb.SendRequest) (*messagepb.SendResponse, error) {
	if c == nil {
		return nil, fmt.Errorf("message client is nil")
	}
	if req == nil {
		return nil, fmt.Errorf("send request must not be nil")
	}

	callCtx := metadata.AppendToOutgoingContext(ctx, ServiceTokenMetadataKey, c.serviceToken)
	cancel := func() {}
	if c.timeout > 0 {
		callCtx, cancel = context.WithTimeout(callCtx, c.timeout)
	}
	defer cancel()

	return c.svc.Send(callCtx, req)
}
