package nat

import (
	"backend-core/internal/agent/config"
	"fmt"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"sync"
)

// Forwarder ensures host-level NAT rules exist for a provisioned guest.
type Forwarder interface {
	EnsureForward(instanceID string, hostPort int, guestIP string) error
	ReleaseForward(instanceID string, hostPort int) error
}

// Runner abstracts command execution for firewall operations.
type Runner interface {
	Run(name string, args ...string) error
	Output(name string, args ...string) (string, error)
}

type execRunner struct{}

func (execRunner) Run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%s %v: %w: %s", name, args, err, string(output))
	}
	return nil
}

func (execRunner) Output(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s %v: %w: %s", name, args, err, string(output))
	}
	return string(output), nil
}

// IPTablesForwarder configures Linux iptables rules for NAT-mode instances.
type IPTablesForwarder struct {
	runner          Runner
	iptablesBinary  string
	sysctlBinary    string
	sshTargetPort   int
	internalNetwork string
	enableOnce      sync.Once
	enableErr       error
}

// NewIPTablesForwarder builds a forwarder from agent NAT config.
func NewIPTablesForwarder(cfg config.NATConfig) *IPTablesForwarder {
	sshTargetPort := cfg.SSHTargetPort
	if sshTargetPort <= 0 {
		sshTargetPort = 22
	}
	return &IPTablesForwarder{
		runner:          execRunner{},
		iptablesBinary:  "iptables",
		sysctlBinary:    "sysctl",
		sshTargetPort:   sshTargetPort,
		internalNetwork: cfg.InternalNetwork,
	}
}

// EnsureForward installs the forwarding and masquerade rules required for a
// NAT-mode instance to be reachable from the host's public IP.
func (f *IPTablesForwarder) EnsureForward(instanceID string, hostPort int, guestIP string) error {
	if hostPort <= 0 || hostPort > 65535 {
		return fmt.Errorf("nat: invalid host port %d", hostPort)
	}
	if instanceID == "" {
		return fmt.Errorf("nat: instance id is required")
	}
	ip := net.ParseIP(guestIP)
	if ip == nil || ip.To4() == nil {
		return fmt.Errorf("nat: invalid guest IPv4 %q", guestIP)
	}
	if err := f.ensureIPForwarding(); err != nil {
		return err
	}
	port := strconv.Itoa(hostPort)
	targetPort := strconv.Itoa(f.sshTargetPort)
	target := net.JoinHostPort(guestIP, targetPort)
	commentArgs := []string{"-m", "comment", "--comment", ruleComment(instanceID, hostPort)}

	if f.internalNetwork != "" {
		if err := f.ensureRule("-t", "nat", "POSTROUTING", "-s", f.internalNetwork, "-j", "MASQUERADE"); err != nil {
			return err
		}
	}
	if err := f.ensureRule(append([]string{"-t", "nat", "PREROUTING", "-p", "tcp", "--dport", port}, append(commentArgs, "-j", "DNAT", "--to-destination", target)...)...); err != nil {
		return err
	}
	if err := f.ensureRule(append([]string{"-t", "nat", "OUTPUT", "-p", "tcp", "--dport", port}, append(commentArgs, "-j", "DNAT", "--to-destination", target)...)...); err != nil {
		return err
	}
	if err := f.ensureRule(append([]string{"FORWARD", "-p", "tcp", "-d", guestIP, "--dport", targetPort}, append(commentArgs, "-j", "ACCEPT")...)...); err != nil {
		return err
	}
	if err := f.ensureRule(append([]string{"FORWARD", "-p", "tcp", "-s", guestIP, "--sport", targetPort}, append(commentArgs, "-j", "ACCEPT")...)...); err != nil {
		return err
	}
	return nil
}

// ReleaseForward removes the per-instance DNAT and FORWARD rules created for a NAT-mode guest.
func (f *IPTablesForwarder) ReleaseForward(instanceID string, hostPort int) error {
	if instanceID == "" || hostPort <= 0 {
		return nil
	}
	comment := ruleComment(instanceID, hostPort)
	if err := f.deleteRulesByComment("nat", "PREROUTING", comment); err != nil {
		return err
	}
	if err := f.deleteRulesByComment("nat", "OUTPUT", comment); err != nil {
		return err
	}
	if err := f.deleteRulesByComment("", "FORWARD", comment); err != nil {
		return err
	}
	return nil
}

func (f *IPTablesForwarder) ensureIPForwarding() error {
	f.enableOnce.Do(func() {
		f.enableErr = f.runner.Run(f.sysctlBinary, "-w", "net.ipv4.ip_forward=1")
		if f.enableErr != nil {
			f.enableErr = fmt.Errorf("nat: enable ip_forward: %w", f.enableErr)
		}
	})
	return f.enableErr
}

func (f *IPTablesForwarder) ensureRule(args ...string) error {
	checkArgs := append([]string{"-C"}, args...)
	if err := f.runner.Run(f.iptablesBinary, checkArgs...); err == nil {
		return nil
	}
	addArgs := append([]string{"-A"}, args...)
	if err := f.runner.Run(f.iptablesBinary, addArgs...); err != nil {
		return fmt.Errorf("nat: ensure iptables rule %v: %w", args, err)
	}
	return nil
}

func (f *IPTablesForwarder) deleteRulesByComment(table, chain, comment string) error {
	listArgs := []string{}
	if table != "" {
		listArgs = append(listArgs, "-t", table)
	}
	listArgs = append(listArgs, "-S", chain)
	output, err := f.runner.Output(f.iptablesBinary, listArgs...)
	if err != nil {
		return fmt.Errorf("nat: list iptables rules for %s/%s: %w", table, chain, err)
	}
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.Contains(line, comment) {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 || fields[0] != "-A" || fields[1] != chain {
			continue
		}
		deleteArgs := []string{}
		if table != "" {
			deleteArgs = append(deleteArgs, "-t", table)
		}
		deleteArgs = append(deleteArgs, "-D")
		deleteArgs = append(deleteArgs, fields[1:]...)
		if err := f.runner.Run(f.iptablesBinary, deleteArgs...); err != nil {
			return fmt.Errorf("nat: delete iptables rule %q: %w", line, err)
		}
	}
	return nil
}

func ruleComment(instanceID string, hostPort int) string {
	return fmt.Sprintf("celeris-nat:%s:%d", instanceID, hostPort)
}
