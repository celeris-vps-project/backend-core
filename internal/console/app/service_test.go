package app

import (
	"backend-core/internal/instance/domain"
	"backend-core/pkg/contracts"
	"errors"
	"testing"
	"time"
)

type fakeInstanceRepo struct {
	inst *domain.Instance
}

func (r *fakeInstanceRepo) GetByID(id string) (*domain.Instance, error) {
	if r.inst != nil && r.inst.ID() == id {
		return r.inst, nil
	}
	return nil, errors.New("instance not found")
}

type fakeRuntimeReader struct{}

func (r fakeRuntimeReader) GetInstanceRuntimeState(instanceID, nodeID string) (contracts.InstanceRuntimeState, bool) {
	return contracts.InstanceRuntimeState{
		InstanceID: instanceID,
		State:      domain.InstanceStatusRunning,
	}, true
}

func TestCreateSessionWaitsForVncTicket(t *testing.T) {
	inst, err := domain.NewInstance("ins-1", "cust-1", "ord-1", "node-1", "web-01", "plan-a", "ubuntu-24.04", "", 2, 2048, 40, 100)
	if err != nil {
		t.Fatalf("new instance: %v", err)
	}
	if err := inst.MarkProvisioned(time.Now()); err != nil {
		t.Fatalf("mark provisioned: %v", err)
	}

	svc := NewService(&fakeInstanceRepo{inst: inst}, fakeRuntimeReader{})
	done := make(chan *Session, 1)
	errCh := make(chan error, 1)

	go func() {
		session, err := svc.CreateSession("ins-1", "cust-1", false)
		if err != nil {
			errCh <- err
			return
		}
		done <- session
	}()

	var sessionID string
	deadline := time.Now().Add(2 * time.Second)
	for sessionID == "" {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for console session to be registered")
		}
		svc.mu.Lock()
		for id := range svc.sessions {
			sessionID = id
			break
		}
		svc.mu.Unlock()
		if sessionID == "" {
			time.Sleep(10 * time.Millisecond)
		}
	}

	svc.setVncTicket(sessionID, "vnc-ticket-123")

	select {
	case err := <-errCh:
		t.Fatalf("create session failed: %v", err)
	case session := <-done:
		if session.VncTicket != "vnc-ticket-123" {
			t.Fatalf("expected vnc ticket to be propagated, got %q", session.VncTicket)
		}
		if session.Ticket == "" {
			t.Fatal("expected browser ticket to be generated")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for session result")
	}
}
