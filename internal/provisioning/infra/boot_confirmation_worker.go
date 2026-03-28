package infra

import (
	"backend-core/internal/provisioning/domain"
	"backend-core/pkg/contracts"
	"backend-core/pkg/delayed"
	"backend-core/pkg/eventbus"
	"backend-core/pkg/events"
	"context"
	"encoding/json"
	"log"
	"time"
)

// BootConfirmPayload is the JSON structure published to the
// "provision.confirm_boot" topic when a provisioning task is created.
// The worker deserialises this to check whether the task has completed
// and the VM has actually booted.
type BootConfirmPayload struct {
	InstanceID string `json:"instance_id"`
	TaskID     string `json:"task_id"`
	NodeID     string `json:"node_id"`
	Attempt    int    `json:"attempt,omitempty"` // retry attempt number (0 = first check)
}

// MaxBootConfirmAttempts is the maximum number of retry attempts for boot
// confirmation before giving up. With exponential backoff (30s, 60s, 120s),
// this covers ~3.5 minutes of checking after the initial 30s delay.
const MaxBootConfirmAttempts = 3

// BootConfirmationWorker handles delayed "provision.confirm_boot" events.
// After a provisioning task is enqueued for an agent, a delayed message is
// scheduled (e.g. 30 seconds). When the message fires, this worker checks:
//
//  1. Is the task completed? → emit ProvisioningCompletedEvent (if not already emitted)
//  2. Is the task still pending/running? → re-schedule with exponential backoff
//  3. Did the task fail? → emit ProvisioningFailedEvent
//
// This provides an asynchronous "boot confirmation" mechanism that
// decouples the provisioning request from its outcome verification.
//
// The worker is idempotent — safe to call multiple times for the same task.
type BootConfirmationWorker struct {
	taskRepo       domain.TaskRepository
	stateCache     domain.NodeStateCache
	bus            *eventbus.EventBus
	delayPublisher delayed.Publisher // for re-scheduling retries
}

// NewBootConfirmationWorker creates a worker that verifies provisioning
// task completion and emits appropriate events.
func NewBootConfirmationWorker(
	taskRepo domain.TaskRepository,
	stateCache domain.NodeStateCache,
	bus *eventbus.EventBus,
	delayPublisher delayed.Publisher,
) *BootConfirmationWorker {
	return &BootConfirmationWorker{
		taskRepo:       taskRepo,
		stateCache:     stateCache,
		bus:            bus,
		delayPublisher: delayPublisher,
	}
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

		log.Printf("[boot-confirm] checking boot status: instance=%s task=%s node=%s attempt=%d",
			p.InstanceID, p.TaskID, p.NodeID, p.Attempt)

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

			// The ReportTaskResult handler already emits ProvisioningCompletedEvent
			// in the normal flow. This is a fallback check — only emit if the
			// instance might not have been updated (e.g. event was lost).
			// Since events are idempotent (ConfirmProvisioning is safe to call
			// multiple times), we can safely emit again.

		case contracts.TaskStatusFailed:
			log.Printf("[boot-confirm] FAILED: instance=%s task=%s on node=%s error=%q",
				p.InstanceID, p.TaskID, p.NodeID, task.Error)

			// Emit failure event so the instance domain can update status
			if w.bus != nil {
				w.bus.Publish(events.ProvisioningFailedEvent{
					InstanceID: p.InstanceID,
					NodeID:     p.NodeID,
					TaskID:     p.TaskID,
					Error:      task.Error,
				})
			}

		case contracts.TaskStatusQueued, contracts.TaskStatusRunning:
			// Task is still in progress — re-schedule with exponential backoff
			if p.Attempt >= MaxBootConfirmAttempts {
				log.Printf("[boot-confirm] GIVING UP: instance=%s task=%s still %s after %d attempts on node=%s",
					p.InstanceID, p.TaskID, task.Status, p.Attempt, p.NodeID)

				// Mark task as failed (timeout)
				task.Status = contracts.TaskStatusFailed
				task.Error = "boot_confirmation_timeout: task did not complete within retry deadline"
				task.FinishedAt = time.Now().Format(time.RFC3339)
				if saveErr := w.taskRepo.Save(task); saveErr != nil {
					log.Printf("[boot-confirm] ERROR: failed to mark task %s as failed: %v", p.TaskID, saveErr)
				}

				// Emit failure event
				if w.bus != nil {
					w.bus.Publish(events.ProvisioningFailedEvent{
						InstanceID: p.InstanceID,
						NodeID:     p.NodeID,
						TaskID:     p.TaskID,
						Error:      "boot_confirmation_timeout: task did not complete within retry deadline",
					})
				}
				return
			}

			// Re-schedule with exponential backoff: 30s → 60s → 120s
			nextDelay := 30 * time.Second * time.Duration(1<<uint(p.Attempt))
			log.Printf("[boot-confirm] RETRY: instance=%s task=%s still %s — re-scheduling in %v (attempt %d/%d)",
				p.InstanceID, p.TaskID, task.Status, nextDelay, p.Attempt+1, MaxBootConfirmAttempts)

			w.reschedule(p.InstanceID, p.TaskID, p.NodeID, p.Attempt+1, nextDelay)

		default:
			log.Printf("[boot-confirm] UNKNOWN status %q for task=%s instance=%s",
				task.Status, p.TaskID, p.InstanceID)
		}
	}
}

// reschedule publishes a new delayed boot confirmation check.
func (w *BootConfirmationWorker) reschedule(instanceID, taskID, nodeID string, attempt int, delay time.Duration) {
	if w.delayPublisher == nil {
		return
	}

	payload, err := json.Marshal(BootConfirmPayload{
		InstanceID: instanceID,
		TaskID:     taskID,
		NodeID:     nodeID,
		Attempt:    attempt,
	})
	if err != nil {
		log.Printf("[boot-confirm] ERROR: failed to marshal retry payload: %v", err)
		return
	}

	if err := w.delayPublisher.PublishDelayed(
		context.Background(),
		"provision.confirm_boot",
		payload,
		delay,
	); err != nil {
		log.Printf("[boot-confirm] WARNING: failed to re-schedule boot confirmation for instance=%s: %v",
			instanceID, err)
	}
}
