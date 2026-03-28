package infra

import (
	"backend-core/internal/provisioning/domain"
	"backend-core/pkg/contracts"
	"backend-core/pkg/eventbus"
	"backend-core/pkg/events"
	"context"
	"log"
	"time"
)

// ProvisionPoller is a background worker that periodically checks pending
// provisioning tasks and ensures instance state is synchronized. It acts
// as a safety net in case the agent's ReportTaskResult callback was missed
// (e.g. network partition, agent crash).
//
// Every pollInterval (default 10s), the poller:
//  1. Lists all tasks with status=queued or status=running
//  2. For tasks older than staleThreshold: marks as failed (stuck task)
//  3. For completed tasks where the instance wasn't updated: emits events
//
// The poller is idempotent — safe to run on multiple controller instances
// (though only one should be active for efficiency).
type ProvisionPoller struct {
	taskRepo       domain.TaskRepository
	stateCache     domain.NodeStateCache
	bus            *eventbus.EventBus
	pollInterval   time.Duration
	staleThreshold time.Duration // how long before a queued/running task is considered stuck
}

// ProvisionPollerConfig holds configuration for the poller.
type ProvisionPollerConfig struct {
	PollInterval   time.Duration // default: 10s
	StaleThreshold time.Duration // default: 10min
}

// NewProvisionPoller creates a new provision poller.
func NewProvisionPoller(
	taskRepo domain.TaskRepository,
	stateCache domain.NodeStateCache,
	bus *eventbus.EventBus,
	cfg ProvisionPollerConfig,
) *ProvisionPoller {
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 10 * time.Second
	}
	if cfg.StaleThreshold <= 0 {
		cfg.StaleThreshold = 10 * time.Minute
	}
	return &ProvisionPoller{
		taskRepo:       taskRepo,
		stateCache:     stateCache,
		bus:            bus,
		pollInterval:   cfg.PollInterval,
		staleThreshold: cfg.StaleThreshold,
	}
}

// Start launches the poller in a goroutine. It blocks until ctx is cancelled.
func (p *ProvisionPoller) Start(ctx context.Context) {
	go func() {
		log.Printf("[provision-poller] started (interval=%v, stale_threshold=%v)", p.pollInterval, p.staleThreshold)
		ticker := time.NewTicker(p.pollInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				log.Printf("[provision-poller] stopped")
				return
			case <-ticker.C:
				p.poll()
			}
		}
	}()
}

// poll runs a single polling cycle.
func (p *ProvisionPoller) poll() {
	// Get all online nodes from cache and check their pending tasks
	states, err := p.stateCache.GetAllNodeStates()
	if err != nil {
		log.Printf("[provision-poller] ERROR: failed to get node states: %v", err)
		return
	}

	for nodeID := range states {
		p.checkNodeTasks(nodeID)
	}
}

// checkNodeTasks checks pending tasks for a specific node.
func (p *ProvisionPoller) checkNodeTasks(nodeID string) {
	tasks, err := p.taskRepo.ListPendingByNodeID(nodeID)
	if err != nil {
		return
	}

	now := time.Now()
	for _, task := range tasks {
		createdAt, parseErr := time.Parse(time.RFC3339, task.CreatedAt)
		if parseErr != nil {
			continue
		}

		age := now.Sub(createdAt)

		switch task.Status {
		case contracts.TaskStatusQueued:
			// Task has been queued but not picked up by agent
			if age > p.staleThreshold {
				log.Printf("[provision-poller] STALE: task %s (type=%s instance=%s) queued for %v on node %s — marking failed",
					task.ID, task.Type, task.Spec.InstanceID, age.Round(time.Second), nodeID)

				task.Status = contracts.TaskStatusFailed
				task.Error = "provision_timeout: task was not picked up by agent within deadline"
				task.FinishedAt = now.Format(time.RFC3339)
				if saveErr := p.taskRepo.Save(&task); saveErr != nil {
					log.Printf("[provision-poller] ERROR: failed to update stale task %s: %v", task.ID, saveErr)
					continue
				}

				// Emit failure event
				if p.bus != nil && task.Type == contracts.TaskProvision {
					p.bus.Publish(events.ProvisioningFailedEvent{
						InstanceID: task.Spec.InstanceID,
						NodeID:     nodeID,
						TaskID:     task.ID,
						Error:      "provision_timeout: task was not picked up by agent",
					})
				}
			}

		case contracts.TaskStatusRunning:
			// Task was picked up but never finished
			if age > p.staleThreshold*2 {
				log.Printf("[provision-poller] STUCK: task %s (type=%s instance=%s) running for %v on node %s — marking failed",
					task.ID, task.Type, task.Spec.InstanceID, age.Round(time.Second), nodeID)

				task.Status = contracts.TaskStatusFailed
				task.Error = "provision_timeout: task execution exceeded deadline (agent may have crashed)"
				task.FinishedAt = now.Format(time.RFC3339)
				if saveErr := p.taskRepo.Save(&task); saveErr != nil {
					log.Printf("[provision-poller] ERROR: failed to update stuck task %s: %v", task.ID, saveErr)
					continue
				}

				if p.bus != nil && task.Type == contracts.TaskProvision {
					p.bus.Publish(events.ProvisioningFailedEvent{
						InstanceID: task.Spec.InstanceID,
						NodeID:     nodeID,
						TaskID:     task.ID,
						Error:      "provision_timeout: task execution exceeded deadline",
					})
				}
			}
		}
	}
}
