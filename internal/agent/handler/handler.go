package handler

import (
	"backend-core/internal/agent/vm"
	"backend-core/pkg/contracts"
	"log"
	"time"
)

// DefaultBootTimeout is the maximum time to wait for a VM to reach the
// driver's ready state. 5 minutes is generous enough to cover slow starts.
const DefaultBootTimeout = 5 * time.Minute

// NATForwarder applies host-level port forwarding for NAT-mode tasks.
type NATForwarder interface {
	EnsureForward(instanceID string, hostPort int, guestIP string, guestPort int) error
	ReleaseForward(instanceID string, hostPort int) error
}

// ProcessTasks executes any tasks received from the controller heartbeat ack,
// then reports results back. For provision/start tasks, if the driver supports
// BootWaiter, it polls the hypervisor until the VM reaches the driver's ready
// state. If the controller already assigned a static IP, that IP is reported.
func ProcessTasks(tasks []contracts.Task, driver vm.Hypervisor, natForwarder NATForwarder, reportFn func(contracts.TaskResult)) {
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
			reportFn(result)
			continue
		}

		log.Printf("[agent] task %s COMPLETED (execution phase)", task.ID)

		// For provision/start tasks, confirm the VM reached the driver's
		// ready state. Controller-assigned static IPs do not depend on a
		// guest agent reporting network interfaces.
		if needsBootWait(task.Type) {
			if task.Spec.IPv4 != "" {
				result.IPv4 = task.Spec.IPv4
				result.IPv6 = task.Spec.IPv6
				result.VMState = "running"
				log.Printf("[agent] task %s: using provisioned static IPv4=%s (skipping guest-agent IP wait)",
					task.ID, task.Spec.IPv4)
			} else if bw, ok := driver.(vm.BootWaiter); ok {
				log.Printf("[agent] task %s: waiting for boot (polling guest agent)...", task.ID)
				info, waitErr := bw.WaitForBoot(task.Spec.InstanceID, DefaultBootTimeout)
				if waitErr != nil {
					// Boot wait timed out — task itself succeeded (VM is created/started),
					// but we couldn't get the IP. Report as completed with a warning.
					log.Printf("[agent] task %s: boot wait failed: %v (reporting completed without IP)", task.ID, waitErr)
					result.VMState = "boot_timeout"
				} else {
					result.IPv4 = info.IPv4
					result.IPv6 = info.IPv6
					result.VMState = info.State
					log.Printf("[agent] task %s: boot confirmed ipv4=%s ipv6=%s state=%s",
						task.ID, info.IPv4, info.IPv6, info.State)
				}
			} else {
				// Driver doesn't support BootWaiter — fall back to single Info() call
				if info, infoErr := driver.Info(task.Spec.InstanceID); infoErr == nil {
					result.IPv4 = info.IPv4
					result.IPv6 = info.IPv6
					result.VMState = info.State
					if info.IPv4 != "" || info.IPv6 != "" {
						log.Printf("[agent] task %s instance IP (single query): v4=%s v6=%s",
							task.ID, info.IPv4, info.IPv6)
					}
				}
			}
		}

		if natErr := ensureNATForward(task, result, natForwarder); natErr != nil {
			result.Status = contracts.TaskStatusFailed
			result.Error = natErr.Error()
			log.Printf("[agent] task %s NAT setup FAILED: %v", task.ID, natErr)
		}
		if natErr := releaseNATForward(task, result, natForwarder); natErr != nil {
			result.Status = contracts.TaskStatusFailed
			result.Error = natErr.Error()
			log.Printf("[agent] task %s NAT cleanup FAILED: %v", task.ID, natErr)
		}

		reportFn(result)
	}
}

func SyncNATForwards(rules []contracts.NATForwardRule, forwarder NATForwarder) error {
	if forwarder == nil {
		return nil
	}
	for _, rule := range rules {
		if rule.InstanceID == "" || rule.HostPort <= 0 || rule.GuestIP == "" {
			continue
		}
		if err := forwarder.EnsureForward(rule.InstanceID, rule.HostPort, rule.GuestIP, normalizeGuestPort(rule.GuestPort)); err != nil {
			return err
		}
	}
	return nil
}

// needsBootWait returns true for task types that result in a VM being
// started (and thus need boot confirmation polling).
func needsBootWait(tt contracts.TaskType) bool {
	switch tt {
	case contracts.TaskProvision, contracts.TaskStart, contracts.TaskReboot, contracts.TaskUnsuspend:
		return true
	default:
		return false
	}
}

func ensureNATForward(task contracts.Task, result contracts.TaskResult, forwarder NATForwarder) error {
	if task.Spec.NetworkMode != contracts.NetworkModeNAT {
		return nil
	}
	if result.Status != contracts.TaskStatusCompleted {
		return nil
	}
	if task.Spec.NATPort <= 0 {
		return nil
	}
	if forwarder == nil {
		return nil
	}
	guestIP := result.IPv4
	if guestIP == "" {
		guestIP = task.Spec.IPv4
	}
	if guestIP == "" {
		return nil
	}
	for _, rule := range natForwardRulesForTask(task, guestIP) {
		if err := forwarder.EnsureForward(rule.InstanceID, rule.HostPort, rule.GuestIP, normalizeGuestPort(rule.GuestPort)); err != nil {
			return err
		}
	}
	return nil
}

func releaseNATForward(task contracts.Task, result contracts.TaskResult, forwarder NATForwarder) error {
	if task.Type != contracts.TaskDeprovision {
		return nil
	}
	if task.Spec.NetworkMode != contracts.NetworkModeNAT {
		return nil
	}
	if result.Status != contracts.TaskStatusCompleted {
		return nil
	}
	if forwarder == nil {
		return nil
	}
	for _, rule := range natForwardRulesForTask(task, task.Spec.IPv4) {
		if rule.HostPort <= 0 {
			continue
		}
		if err := forwarder.ReleaseForward(task.Spec.InstanceID, rule.HostPort); err != nil {
			return err
		}
	}
	return nil
}

func natForwardRulesForTask(task contracts.Task, guestIP string) []contracts.NATForwardRule {
	if len(task.Spec.NATForwards) > 0 {
		rules := make([]contracts.NATForwardRule, 0, len(task.Spec.NATForwards))
		for _, rule := range task.Spec.NATForwards {
			if rule.InstanceID == "" {
				rule.InstanceID = task.Spec.InstanceID
			}
			if rule.GuestIP == "" {
				rule.GuestIP = guestIP
			}
			rule.GuestPort = normalizeGuestPort(rule.GuestPort)
			rules = append(rules, rule)
		}
		return rules
	}
	if task.Spec.NATPort <= 0 {
		return nil
	}
	return []contracts.NATForwardRule{{
		InstanceID: task.Spec.InstanceID,
		HostPort:   task.Spec.NATPort,
		GuestIP:    guestIP,
		GuestPort:  22,
		Protocol:   "tcp",
	}}
}

func normalizeGuestPort(port int) int {
	if port <= 0 {
		return 22
	}
	return port
}
