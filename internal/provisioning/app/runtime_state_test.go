package app

import (
	"backend-core/pkg/contracts"
	"testing"
)

func TestRuntimeStateFromTaskResultPreservesVMTransferred(t *testing.T) {
	task := &contracts.Task{
		Type: contracts.TaskProvision,
		Spec: contracts.ProvisionSpec{
			InstanceID: "inst-1",
			IPv4:       "10.0.0.11",
		},
	}
	result := contracts.TaskResult{
		Status:     contracts.TaskStatusCompleted,
		FinishedAt: "2026-05-06T10:00:00Z",
		VMInfo: contracts.VMInfo{
			VMTransferred: contracts.VMTransferred{
				Total: 321,
				RX:    111,
				TX:    222,
			},
		},
	}

	got, ok := runtimeStateFromTaskResult(task, result)
	if !ok {
		t.Fatal("expected runtime state to be produced")
	}
	if got.VMTransferred.Total != 321 || got.VMTransferred.RX != 111 || got.VMTransferred.TX != 222 {
		t.Fatalf("unexpected vm_transferred mapping: total=%d rx=%d tx=%d", got.VMTransferred.Total, got.VMTransferred.RX, got.VMTransferred.TX)
	}
}
