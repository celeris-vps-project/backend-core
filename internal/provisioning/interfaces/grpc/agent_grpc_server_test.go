package grpc

import (
	"backend-core/pkg/agentpb"
	"testing"
)

func TestProtoToRuntimeStatesPreservesVMTransferred(t *testing.T) {
	got := protoToRuntimeStates([]*agentpb.VMState{
		{
			InstanceId: "inst-1",
			State:      "running",
			VmTransferred: &agentpb.VMTransferred{
				Total: 123,
				Tx:    67,
				Rx:    45,
			},
		},
	})
	if len(got) != 1 {
		t.Fatalf("expected 1 runtime state, got %d", len(got))
	}
	vt := got[0].VMTransferred
	if vt.Total != 123 || vt.TX != 67 || vt.RX != 45 {
		t.Fatalf("unexpected vm_transferred mapping: total=%d tx=%d rx=%d", vt.Total, vt.TX, vt.RX)
	}
}
