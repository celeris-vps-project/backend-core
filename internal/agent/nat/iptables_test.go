package nat

import (
	"backend-core/internal/agent/config"
	"errors"
	"strings"
	"testing"
)

type recordedCommand struct {
	name string
	args []string
}

type fakeRunner struct {
	commands []recordedCommand
	outputs  map[string]string
}

func (r *fakeRunner) Run(name string, args ...string) error {
	r.commands = append(r.commands, recordedCommand{name: name, args: append([]string(nil), args...)})
	if len(args) > 0 && args[0] == "-C" {
		return errors.New("rule not found")
	}
	return nil
}

func (r *fakeRunner) Output(name string, args ...string) (string, error) {
	r.commands = append(r.commands, recordedCommand{name: name, args: append([]string(nil), args...)})
	if r.outputs == nil {
		return "", nil
	}
	return r.outputs[name+" "+strings.Join(args, " ")], nil
}

func TestIPTablesForwarder_EnsureForwardInstallsRules(t *testing.T) {
	runner := &fakeRunner{}
	forwarder := NewIPTablesForwarder(config.NATConfig{
		SSHTargetPort:   22,
		InternalNetwork: "10.0.0.0/24",
	})
	forwarder.runner = runner

	if err := forwarder.EnsureForward("inst-1", 20001, "10.0.0.15"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(runner.commands) == 0 {
		t.Fatal("expected firewall commands to be executed")
	}
	if runner.commands[0].name != "sysctl" || !strings.Contains(strings.Join(runner.commands[0].args, " "), "net.ipv4.ip_forward=1") {
		t.Fatalf("expected sysctl ip_forward command first, got %s %v", runner.commands[0].name, runner.commands[0].args)
	}

	assertCommandContains(t, runner.commands, "iptables", []string{"-A", "-t", "nat", "POSTROUTING", "-s", "10.0.0.0/24", "-j", "MASQUERADE"})
	assertCommandContains(t, runner.commands, "iptables", []string{"-A", "-t", "nat", "PREROUTING", "-p", "tcp", "--dport", "20001", "-m", "comment", "--comment", "celeris-nat:inst-1:20001", "-j", "DNAT", "--to-destination", "10.0.0.15:22"})
	assertCommandContains(t, runner.commands, "iptables", []string{"-A", "-t", "nat", "OUTPUT", "-p", "tcp", "--dport", "20001", "-m", "comment", "--comment", "celeris-nat:inst-1:20001", "-j", "DNAT", "--to-destination", "10.0.0.15:22"})
	assertCommandContains(t, runner.commands, "iptables", []string{"-A", "FORWARD", "-p", "tcp", "-d", "10.0.0.15", "--dport", "22", "-m", "comment", "--comment", "celeris-nat:inst-1:20001", "-j", "ACCEPT"})
	assertCommandContains(t, runner.commands, "iptables", []string{"-A", "FORWARD", "-p", "tcp", "-s", "10.0.0.15", "--sport", "22", "-m", "comment", "--comment", "celeris-nat:inst-1:20001", "-j", "ACCEPT"})
}

func TestIPTablesForwarder_ReleaseForwardDeletesTaggedRules(t *testing.T) {
	runner := &fakeRunner{
		outputs: map[string]string{
			"iptables -t nat -S PREROUTING": "-A PREROUTING -p tcp --dport 20001 -m comment --comment celeris-nat:inst-1:20001 -j DNAT --to-destination 10.0.0.15:22\n",
			"iptables -t nat -S OUTPUT":     "-A OUTPUT -p tcp --dport 20001 -m comment --comment celeris-nat:inst-1:20001 -j DNAT --to-destination 10.0.0.15:22\n",
			"iptables -S FORWARD": "-A FORWARD -p tcp -d 10.0.0.15 --dport 22 -m comment --comment celeris-nat:inst-1:20001 -j ACCEPT\n" +
				"-A FORWARD -p tcp -s 10.0.0.15 --sport 22 -m comment --comment celeris-nat:inst-1:20001 -j ACCEPT\n",
		},
	}
	forwarder := NewIPTablesForwarder(config.NATConfig{SSHTargetPort: 22})
	forwarder.runner = runner

	if err := forwarder.ReleaseForward("inst-1", 20001); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	assertCommandContains(t, runner.commands, "iptables", []string{"-t", "nat", "-D", "PREROUTING", "-p", "tcp", "--dport", "20001", "-m", "comment", "--comment", "celeris-nat:inst-1:20001", "-j", "DNAT", "--to-destination", "10.0.0.15:22"})
	assertCommandContains(t, runner.commands, "iptables", []string{"-t", "nat", "-D", "OUTPUT", "-p", "tcp", "--dport", "20001", "-m", "comment", "--comment", "celeris-nat:inst-1:20001", "-j", "DNAT", "--to-destination", "10.0.0.15:22"})
	assertCommandContains(t, runner.commands, "iptables", []string{"-D", "FORWARD", "-p", "tcp", "-d", "10.0.0.15", "--dport", "22", "-m", "comment", "--comment", "celeris-nat:inst-1:20001", "-j", "ACCEPT"})
	assertCommandContains(t, runner.commands, "iptables", []string{"-D", "FORWARD", "-p", "tcp", "-s", "10.0.0.15", "--sport", "22", "-m", "comment", "--comment", "celeris-nat:inst-1:20001", "-j", "ACCEPT"})
}

func assertCommandContains(t *testing.T, commands []recordedCommand, name string, expectedArgs []string) {
	t.Helper()
	for _, cmd := range commands {
		if cmd.name != name {
			continue
		}
		if len(cmd.args) != len(expectedArgs) {
			continue
		}
		matched := true
		for i := range expectedArgs {
			if cmd.args[i] != expectedArgs[i] {
				matched = false
				break
			}
		}
		if matched {
			return
		}
	}
	t.Fatalf("expected command %s %v, got %#v", name, expectedArgs, commands)
}
