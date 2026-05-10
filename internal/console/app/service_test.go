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

func TestCreateSessionReturnsBeforeVncTicket(t *testing.T) {
	inst, err := domain.NewInstance("ins-1", "cust-1", "ord-1", "node-1", "web-01", "plan-a", "ubuntu-24.04", "", 2, 2048, 40, 100)
	if err != nil {
		t.Fatalf("new instance: %v", err)
	}
	if err := inst.MarkProvisioned(time.Now()); err != nil {
		t.Fatalf("mark provisioned: %v", err)
	}

	svc := NewService(&fakeInstanceRepo{inst: inst}, fakeRuntimeReader{})
	session, err := svc.CreateSession("ins-1", "cust-1", false)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if session.VncTicket != "" {
		t.Fatalf("expected initial vnc ticket to be empty, got %q", session.VncTicket)
	}
	if session.Ticket == "" {
		t.Fatal("expected browser ticket to be generated")
	}
	snapshot, err := svc.GetSession(session.ID, "ins-1", "cust-1", false)
	if err != nil {
		t.Fatalf("get session before ticket: %v", err)
	}
	if snapshot.VncTicket != "" {
		t.Fatalf("expected pending vnc ticket to be empty, got %q", snapshot.VncTicket)
	}

	svc.setVncTicket(session.ID, "vnc-ticket-123")
	snapshot, err = svc.GetSession(session.ID, "ins-1", "cust-1", false)
	if err != nil {
		t.Fatalf("get session after ticket: %v", err)
	}
	if snapshot.VncTicket != "vnc-ticket-123" {
		t.Fatalf("expected vnc ticket to be propagated, got %q", snapshot.VncTicket)
	}
}
