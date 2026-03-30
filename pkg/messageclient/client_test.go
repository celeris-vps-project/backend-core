package messageclient

import (
	"backend-core/pkg/messagepb"
	"context"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/test/bufconn"
)

func TestClientSend_AttachesServiceToken(t *testing.T) {
	const serviceToken = "expected-token"

	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	service := &capturingMessageServiceServer{
		sendFunc: func(ctx context.Context, req *messagepb.SendRequest) (*messagepb.SendResponse, error) {
			md, _ := metadata.FromIncomingContext(ctx)
			if got := md.Get(ServiceTokenMetadataKey); len(got) != 1 || got[0] != serviceToken {
				t.Fatalf("service token metadata mismatch: %v", got)
			}
			if req.GetChannel() != "IN_APP" {
				t.Fatalf("channel mismatch: %s", req.GetChannel())
			}
			return &messagepb.SendResponse{Success: true, MessageId: "msg-1"}, nil
		},
	}
	messagepb.RegisterMessageServiceServer(server, service)
	go func() {
		_ = server.Serve(listener)
	}()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
	})

	conn, err := grpc.DialContext(
		context.Background(),
		"bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return listener.Dial()
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("dial bufconn: %v", err)
	}
	t.Cleanup(func() {
		_ = conn.Close()
	})

	client, err := newClient(conn, serviceToken, time.Second)
	if err != nil {
		t.Fatalf("newClient error: %v", err)
	}

	resp, err := client.Send(context.Background(), &messagepb.SendRequest{
		Channel:   "IN_APP",
		Recipient: "user-1",
		Content:   "hello",
		BizId:     "biz-1",
	})
	if err != nil {
		t.Fatalf("Send error: %v", err)
	}
	if !resp.GetSuccess() || resp.GetMessageId() != "msg-1" {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

func TestUserRegisteredNotifier_UsesInAppRecipientAndBizID(t *testing.T) {
	sender := &capturingSender{
		response: &messagepb.SendResponse{Success: true, MessageId: "msg-2"},
	}
	notifier := NewUserRegisteredNotifier(sender, UserRegisteredNotifierConfig{
		Enabled: true,
		Channel: "IN_APP",
		Subject: "Welcome",
		Content: "Hello there",
	})

	if err := notifier.NotifyUserRegistered(context.Background(), "user-42", "user@example.com"); err != nil {
		t.Fatalf("NotifyUserRegistered error: %v", err)
	}
	if sender.request == nil {
		t.Fatal("expected sender to receive a request")
	}
	if sender.request.GetRecipient() != "user-42" {
		t.Fatalf("recipient mismatch: %s", sender.request.GetRecipient())
	}
	if sender.request.GetBizId() != "identity.user_registered:user-42" {
		t.Fatalf("biz id mismatch: %s", sender.request.GetBizId())
	}
}

type capturingSender struct {
	request  *messagepb.SendRequest
	response *messagepb.SendResponse
	err      error
}

func (s *capturingSender) Send(ctx context.Context, req *messagepb.SendRequest) (*messagepb.SendResponse, error) {
	s.request = req
	return s.response, s.err
}

type capturingMessageServiceServer struct {
	messagepb.UnimplementedMessageServiceServer
	sendFunc func(ctx context.Context, req *messagepb.SendRequest) (*messagepb.SendResponse, error)
}

func (s *capturingMessageServiceServer) Send(ctx context.Context, req *messagepb.SendRequest) (*messagepb.SendResponse, error) {
	return s.sendFunc(ctx, req)
}
