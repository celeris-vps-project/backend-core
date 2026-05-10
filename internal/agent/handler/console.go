package handler

import (
	"backend-core/internal/agent/client"
	"backend-core/internal/agent/vm"
	"backend-core/pkg/contracts"
	"context"
	"io"
	"log"
	"sync"
)

func ProcessConsoleSessions(ctx context.Context, sessions []contracts.ConsoleSession, driver vm.Hypervisor, grpcClient *client.AgentClient) {
	if len(sessions) == 0 || grpcClient == nil {
		return
	}
	connector, ok := driver.(vm.ConsoleConnector)
	if !ok {
		for _, session := range sessions {
			reportConsoleError(ctx, grpcClient, session, "console is not supported by this agent backend")
		}
		return
	}
	for _, session := range sessions {
		if session.SessionID == "" || session.InstanceID == "" {
			continue
		}
		go handleConsoleSession(ctx, session, connector, grpcClient)
	}
}

func handleConsoleSession(ctx context.Context, session contracts.ConsoleSession, connector vm.ConsoleConnector, grpcClient *client.AgentClient) {
	stream, err := grpcClient.OpenConsole(ctx)
	if err != nil {
		log.Printf("[agent] console %s open stream failed: %v", session.SessionID, err)
		return
	}
	defer stream.CloseSend()

	if err := stream.Send(contracts.ConsoleFrame{
		SessionID:  session.SessionID,
		InstanceID: session.InstanceID,
		Control:    "open",
	}); err != nil {
		log.Printf("[agent] console %s send open failed: %v", session.SessionID, err)
		return
	}

	vnc, err := connector.OpenConsole(session.InstanceID)
	if err != nil {
		_ = stream.Send(contracts.ConsoleFrame{
			SessionID:  session.SessionID,
			InstanceID: session.InstanceID,
			Error:      err.Error(),
			Control:    "close",
		})
		return
	}
	defer vnc.Close()

	if err := stream.Send(contracts.ConsoleFrame{
		SessionID:  session.SessionID,
		InstanceID: session.InstanceID,
		Control:    "ready",
	}); err != nil {
		return
	}

	done := make(chan struct{})
	var once sync.Once
	closeDone := func() { once.Do(func() { close(done) }) }

	go func() {
		defer closeDone()
		buf := make([]byte, 32*1024)
		for {
			n, err := vnc.Read(buf)
			if n > 0 {
				payload := append([]byte(nil), buf[:n]...)
				if sendErr := stream.Send(contracts.ConsoleFrame{
					SessionID:  session.SessionID,
					InstanceID: session.InstanceID,
					Data:       payload,
				}); sendErr != nil {
					return
				}
			}
			if err != nil {
				if err != io.EOF {
					log.Printf("[agent] console %s vnc read failed: %v", session.SessionID, err)
				}
				return
			}
		}
	}()

	go func() {
		defer closeDone()
		for {
			frame, err := stream.Recv()
			if err != nil {
				return
			}
			if frame.Control == "close" {
				return
			}
			if len(frame.Data) == 0 {
				continue
			}
			if _, err := vnc.Write(frame.Data); err != nil {
				log.Printf("[agent] console %s vnc write failed: %v", session.SessionID, err)
				return
			}
		}
	}()

	select {
	case <-ctx.Done():
	case <-done:
	}
	_ = stream.Send(contracts.ConsoleFrame{
		SessionID:  session.SessionID,
		InstanceID: session.InstanceID,
		Control:    "close",
	})
}

func reportConsoleError(ctx context.Context, grpcClient *client.AgentClient, session contracts.ConsoleSession, message string) {
	stream, err := grpcClient.OpenConsole(ctx)
	if err != nil {
		return
	}
	defer stream.CloseSend()
	_ = stream.Send(contracts.ConsoleFrame{
		SessionID:  session.SessionID,
		InstanceID: session.InstanceID,
		Control:    "open",
	})
	_ = stream.Send(contracts.ConsoleFrame{
		SessionID:  session.SessionID,
		InstanceID: session.InstanceID,
		Error:      message,
		Control:    "close",
	})
}
