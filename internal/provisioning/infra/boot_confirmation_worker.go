package infra

import (
	"backend-core/internal/provisioning/domain"
	"backend-core/pkg/contracts"
	"backend-core/pkg/delayed"
	"encoding/json"
	"log"
)

// BootConfirmPayload is the JSON structure published to the
// "provision.confirm_boot" topic when a provisioning task is created.
// The worker deserialises this to check whether the task has completed
// and the VM has actually booted.
type BootConfirmPayload struct {
	InstanceID string `json:"instance_id"`
	TaskID     string `json:"task_id"`
	NodeID     string `json:"node_id"`
}

// BootConfirmationWorker handles delayed "provision.confirm_boot" events.
// After a provisioning task is enqueued for an agent, a delayed message is
// scheduled (e.g. 30 seconds). When the message fires, this worker checks:
//
//  1. Is the task completed? → log success.
//  2. Is the task still pending/running? → log warning (potential stuck task).
//  3. Did the task fail? → log error for alerting.
//
// This provides an asynchronous "boot confirmation" mechanism that
// decouples the provisioning request from its outcome verification.
//
// The worker is idempotent — safe to call multiple times for the same task.
type BootConfirmationWorker struct {
	taskRepo domain.TaskRepository
}

// NewBootConfirmationWorker creates a worker that verifies provisioning
// task completion.
func NewBootConfirmationWorker(taskRepo domain.TaskRepository) *BootConfirmationWorker {
	return &BootConfirmationWorker{taskRepo: taskRepo}
}

// HandlerFunc returns a delayed.HandlerFunc suitable for registering with
// the delayed.Router:
//
//	router.Handle("provision.confirm_boot", worker.HandlerFunc())
func (w *BootConfirmationWorker) HandlerFunc() delayed.HandlerFunc {
	return func(topic string, payload []byte) {
		var p BootConfirmPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			log.Printf("[boot-confirm] ERROR: failed to unmarshal payload: %v", err)
			return
		}

		log.Printf("[boot-confirm] checking boot status: instance=%s task=%s node=%s",
			p.InstanceID, p.TaskID, p.NodeID)

		task, err := w.taskRepo.GetByID(p.TaskID)
		if err != nil {
			log.Printf("[boot-confirm] WARNING: task %s not found (may have been cleaned up): %v",
				p.TaskID, err)
			return
		}

		switch task.Status {
		case contracts.TaskStatusCompleted:
			log.Printf("[boot-confirm] OK: instance=%s task=%s booted successfully on node=%s",
				p.InstanceID, p.TaskID, p.NodeID)

		case contracts.TaskStatusFailed:
			log.Printf("[boot-confirm] FAILED: instance=%s task=%s on node=%s error=%q",
				p.InstanceID, p.TaskID, p.NodeID, task.Error)
			// TODO: emit an event or alert for manual intervention / auto-retry

		case contracts.TaskStatusQueued, contracts.TaskStatusRunning:
			log.Printf("[boot-confirm] WARNING: instance=%s task=%s still %s after confirmation delay (node=%s)",
				p.InstanceID, p.TaskID, task.Status, p.NodeID)
			// TODO: consider re-scheduling another check or escalating

		default:
			log.Printf("[boot-confirm] UNKNOWN status %q for task=%s instance=%s",
				task.Status, p.TaskID, p.InstanceID)
		}
	}
}
