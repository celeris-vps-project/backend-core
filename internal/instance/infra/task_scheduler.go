package infra

import "backend-core/pkg/contracts"

type taskEnqueuer interface {
	EnqueueTask(nodeID string, taskType contracts.TaskType, spec contracts.ProvisionSpec) (*contracts.Task, error)
}

type ProvisioningTaskScheduler struct {
	enqueuer taskEnqueuer
}

func NewProvisioningTaskScheduler(enqueuer taskEnqueuer) *ProvisioningTaskScheduler {
	return &ProvisioningTaskScheduler{enqueuer: enqueuer}
}

func (s *ProvisioningTaskScheduler) Enqueue(nodeID string, taskType contracts.TaskType, spec contracts.ProvisionSpec) error {
	_, err := s.enqueuer.EnqueueTask(nodeID, taskType, spec)
	return err
}
