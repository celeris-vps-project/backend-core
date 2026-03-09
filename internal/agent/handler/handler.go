package handler

import (
	"backend-core/internal/agent/vm"
	"backend-core/pkg/contracts"
	"log"
	"time"
)

// ProcessTasks executes any tasks received from the controller heartbeat ack,
// then reports results back.
func ProcessTasks(tasks []contracts.Task, driver vm.Hypervisor, reportFn func(contracts.TaskResult)) {
	for _, task := range tasks {
		log.Printf("[agent] executing task %s type=%s instance=%s", task.ID, task.Type, task.Spec.InstanceID)

		err := vm.Execute(driver, task)

		result := contracts.TaskResult{
			TaskID:     task.ID,
			Status:     contracts.TaskStatusCompleted,
			FinishedAt: time.Now().Format(time.RFC3339),
		}
		if err != nil {
			result.Status = contracts.TaskStatusFailed
			result.Error = err.Error()
			log.Printf("[agent] task %s FAILED: %v", task.ID, err)
		} else {
			log.Printf("[agent] task %s COMPLETED", task.ID)
		}

		reportFn(result)
	}
}
