package handler

import (
	"backend-core/internal/agent/vm"
	"backend-core/pkg/contracts"
	"log"
	"time"
)

// ProcessTasks executes any tasks received from the controller heartbeat ack,
// then reports results back. For provision tasks, it also queries the newly
// created instance's IP addresses and includes them in the result.
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

			// For provision tasks, try to retrieve the instance's IP addresses
			// so the controller can record them. The instance may not have an
			// IP yet (DHCP not complete), which is fine — the controller can
			// update it later via subsequent heartbeats or info queries.
			if task.Type == contracts.TaskProvision {
				if info, infoErr := driver.Info(task.Spec.InstanceID); infoErr == nil {
					result.IPv4 = info.IPv4
					result.IPv6 = info.IPv6
					if info.IPv4 != "" || info.IPv6 != "" {
						log.Printf("[agent] task %s instance IP: v4=%s v6=%s", task.ID, info.IPv4, info.IPv6)
					}
				}
			}
		}

		reportFn(result)
	}
}
