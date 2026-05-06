package client

import (
	"backend-core/pkg/contracts"
	"testing"
)

func TestRuntimeStatesToProtoPreservesVMTransferred(t *testing.T) {
	states := []contracts.InstanceRuntimeState{
		{
			InstanceID: "inst-1",
			State:      "running",
			IPv4:       "10.0.0.11",
			VMTransferred: contracts.VMTransferred{
				Total: 123,
				RX:    45,
				TX:    67,
			},
		},
	}

	got := runtimeStatesToProto(states)
	if len(got) != 1 {
		t.Fatalf("expected 1 vm state, got %d", len(got))
	}
	vt := got[0].GetVmTransferred()
	if vt == nil {
		t.Fatal("expected vm_transferred to be populated")
	}
	if vt.GetTotal() != 123 || vt.GetTx() != 67 || vt.GetRx() != 45 {
		t.Fatalf("unexpected vm_transferred mapping: total=%d tx=%d rx=%d", vt.GetTotal(), vt.GetTx(), vt.GetRx())
	}
}
