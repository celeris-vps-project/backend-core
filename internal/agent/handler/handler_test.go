package handler

import (
	"backend-core/internal/agent/vm"
	"backend-core/pkg/contracts"
	"errors"
	"fmt"
	"io"
	"net"
	"testing"
	"time"
)

type fakeDriver struct {
	createdSpec contracts.ProvisionSpec
	waitInfo    *vm.VMInfo
	waitErr     error
	waitCalls   int
}

func (d *fakeDriver) Create(spec contracts.ProvisionSpec) error {
	d.createdSpec = spec
	return nil
}

func (d *fakeDriver) Start(instanceID string) error   { return nil }
func (d *fakeDriver) Stop(instanceID string) error    { return nil }
func (d *fakeDriver) Reboot(instanceID string) error  { return nil }
func (d *fakeDriver) Destroy(instanceID string) error { return nil }
func (d *fakeDriver) Info(instanceID string) (*vm.VMInfo, error) {
	if d.waitInfo != nil {
		return d.waitInfo, nil
	}
	return &vm.VMInfo{InstanceID: instanceID, State: "running"}, nil
}
func (d *fakeDriver) List() ([]*vm.VMInfo, error) { return nil, nil }
func (d *fakeDriver) WaitForBoot(instanceID string, timeout time.Duration) (*vm.VMInfo, error) {
	d.waitCalls++
	if d.waitErr != nil {
		return nil, d.waitErr
	}
	if d.waitInfo != nil {
		return d.waitInfo, nil
	}
	return &vm.VMInfo{InstanceID: instanceID, State: "running"}, nil
}

type fakeForwarder struct {
	hostPort     int
	guestIP      string
	guestPort    int
	releasedPort int
	releasedInst string
	err          error
	releaseErr   error
	calls        int
	releaseCalls int
}

func (f *fakeForwarder) EnsureForward(instanceID string, hostPort int, guestIP string, guestPort int) error {
	f.calls++
	f.hostPort = hostPort
	f.guestIP = guestIP
	f.guestPort = guestPort
	return f.err
}

func (f *fakeForwarder) ReleaseForward(instanceID string, hostPort int) error {
	f.releaseCalls++
	f.releasedInst = instanceID
	f.releasedPort = hostPort
	return f.releaseErr
}

func TestProcessTasks_ProvisionNATCallsForwarder(t *testing.T) {
	driver := &fakeDriver{
		waitInfo: &vm.VMInfo{
			InstanceID: "inst-1",
			State:      "running",
			IPv4:       "10.0.0.15",
		},
	}
	forwarder := &fakeForwarder{}
	task := contracts.Task{
		ID:     "task-1",
		Type:   contracts.TaskProvision,
		Status: contracts.TaskStatusQueued,
		Spec: contracts.ProvisionSpec{
			InstanceID:  "inst-1",
			Hostname:    "web-01",
			OS:          "ubuntu-24.04",
			CPU:         2,
			MemoryMB:    2048,
			DiskGB:      40,
			NetworkMode: contracts.NetworkModeNAT,
			NATPort:     20001,
		},
	}

	var results []contracts.TaskResult
	ProcessTasks([]contracts.Task{task}, driver, forwarder, func(result contracts.TaskResult) {
		results = append(results, result)
	})

	if driver.createdSpec.InstanceID != "inst-1" {
		t.Fatalf("expected create to receive inst-1, got %s", driver.createdSpec.InstanceID)
	}
	if forwarder.calls != 1 {
		t.Fatalf("expected 1 NAT forward call, got %d", forwarder.calls)
	}
	if forwarder.hostPort != 20001 || forwarder.guestIP != "10.0.0.15" || forwarder.guestPort != 22 {
		t.Fatalf("unexpected NAT forward target: port=%d ip=%s guest_port=%d", forwarder.hostPort, forwarder.guestIP, forwarder.guestPort)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 task result, got %d", len(results))
	}
	if results[0].Status != contracts.TaskStatusCompleted {
		t.Fatalf("expected completed status, got %s", results[0].Status)
	}
	if results[0].IPv4 != "10.0.0.15" {
		t.Fatalf("expected IPv4 10.0.0.15, got %s", results[0].IPv4)
	}
}

func TestProcessTasks_ForwarderErrorMarksTaskFailed(t *testing.T) {
	driver := &fakeDriver{
		waitInfo: &vm.VMInfo{
			InstanceID: "inst-2",
			State:      "running",
			IPv4:       "10.0.0.20",
		},
	}
	forwarder := &fakeForwarder{err: errors.New("iptables failed")}
	task := contracts.Task{
		ID:   "task-2",
		Type: contracts.TaskProvision,
		Spec: contracts.ProvisionSpec{
			InstanceID:  "inst-2",
			Hostname:    "web-02",
			OS:          "ubuntu-24.04",
			CPU:         2,
			MemoryMB:    2048,
			DiskGB:      40,
			NetworkMode: contracts.NetworkModeNAT,
			NATPort:     20002,
		},
	}

	var result contracts.TaskResult
	ProcessTasks([]contracts.Task{task}, driver, forwarder, func(r contracts.TaskResult) {
		result = r
	})

	if result.Status != contracts.TaskStatusFailed {
		t.Fatalf("expected failed status, got %s", result.Status)
	}
	if result.Error == "" {
		t.Fatal("expected forwarder error to be reported")
	}
}

func TestProcessTasks_UsesSpecIPv4ForNATWithoutGuestAgentWait(t *testing.T) {
	driver := &fakeDriver{waitErr: errors.New("guest agent timeout")}
	forwarder := &fakeForwarder{}
	task := contracts.Task{
		ID:   "task-spec-ip",
		Type: contracts.TaskProvision,
		Spec: contracts.ProvisionSpec{
			InstanceID:  "inst-spec-ip",
			Hostname:    "web-02",
			OS:          "ubuntu-24.04",
			CPU:         2,
			MemoryMB:    2048,
			DiskGB:      40,
			IPv4:        "10.0.0.30",
			NetworkMode: contracts.NetworkModeNAT,
			NATPort:     20030,
		},
	}

	var result contracts.TaskResult
	ProcessTasks([]contracts.Task{task}, driver, forwarder, func(r contracts.TaskResult) {
		result = r
	})

	if result.Status != contracts.TaskStatusCompleted {
		t.Fatalf("expected completed status, got %s error=%s", result.Status, result.Error)
	}
	if result.VMState != "running" {
		t.Fatalf("expected running state, got %s", result.VMState)
	}
	if result.IPv4 != "10.0.0.30" {
		t.Fatalf("expected result IPv4 10.0.0.30, got %s", result.IPv4)
	}
	if driver.waitCalls != 0 {
		t.Fatalf("expected guest-agent boot wait to be skipped, got %d calls", driver.waitCalls)
	}
	if forwarder.calls != 1 || forwarder.guestIP != "10.0.0.30" {
		t.Fatalf("expected NAT forward to spec IPv4, calls=%d ip=%s", forwarder.calls, forwarder.guestIP)
	}
}

func TestProcessTasks_PreservesVMTransferredStats(t *testing.T) {
	driver := &fakeDriver{
		waitInfo: &vm.VMInfo{
			InstanceID: "inst-stats",
			State:      "running",
			NetworkStats: vm.NetworkStats{
				Total: 321,
				RX:    111,
				TX:    222,
			},
		},
	}
	task := contracts.Task{
		ID:   "task-stats",
		Type: contracts.TaskProvision,
		Spec: contracts.ProvisionSpec{
			InstanceID: "inst-stats",
			Hostname:   "web-stats",
			OS:         "ubuntu-24.04",
			CPU:        2,
			MemoryMB:   2048,
			DiskGB:     40,
		},
	}

	var result contracts.TaskResult
	ProcessTasks([]contracts.Task{task}, driver, nil, func(r contracts.TaskResult) {
		result = r
	})

	if result.VMInfo.VMTransferred.Total != 321 {
		t.Fatalf("expected total traffic 321, got %d", result.VMInfo.VMTransferred.Total)
	}
	if result.VMInfo.VMTransferred.RX != 111 {
		t.Fatalf("expected rx traffic 111, got %d", result.VMInfo.VMTransferred.RX)
	}
	if result.VMInfo.VMTransferred.TX != 222 {
		t.Fatalf("expected tx traffic 222, got %d", result.VMInfo.VMTransferred.TX)
	}
}

func TestIsNormalConsoleReadClose(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "eof", err: io.EOF, want: true},
		{name: "net closed", err: net.ErrClosed, want: true},
		{name: "wrapped net closed", err: fmt.Errorf("read tcp: %w", net.ErrClosed), want: true},
		{name: "legacy closed text", err: errors.New("read tcp 127.0.0.1:42316->127.0.0.1:8006: use of closed network connection"), want: true},
		{name: "real read failure", err: errors.New("connection reset by peer"), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isNormalConsoleReadClose(tt.err); got != tt.want {
				t.Fatalf("expected %v, got %v", tt.want, got)
			}
		})
	}
}

func TestSyncNATForwards_ReplaysDesiredRules(t *testing.T) {
	forwarder := &fakeForwarder{}

	err := SyncNATForwards([]contracts.NATForwardRule{
		{InstanceID: "inst-1", HostPort: 20001, GuestIP: "10.0.0.11", GuestPort: 8080},
	}, forwarder)
	if err != nil {
		t.Fatalf("unexpected sync error: %v", err)
	}
	if forwarder.calls != 1 || forwarder.hostPort != 20001 || forwarder.guestIP != "10.0.0.11" {
		t.Fatalf("unexpected sync target calls=%d port=%d ip=%s", forwarder.calls, forwarder.hostPort, forwarder.guestIP)
	}
	if forwarder.guestPort != 8080 {
		t.Fatalf("expected guest port 8080, got %d", forwarder.guestPort)
	}
}

func TestProcessTasks_DeprovisionNATCallsReleaseHook(t *testing.T) {
	driver := &fakeDriver{}
	forwarder := &fakeForwarder{}
	task := contracts.Task{
		ID:   "task-3",
		Type: contracts.TaskDeprovision,
		Spec: contracts.ProvisionSpec{
			InstanceID:  "inst-3",
			NetworkMode: contracts.NetworkModeNAT,
			NATPort:     20003,
		},
	}

	var result contracts.TaskResult
	ProcessTasks([]contracts.Task{task}, driver, forwarder, func(r contracts.TaskResult) {
		result = r
	})

	if result.Status != contracts.TaskStatusCompleted {
		t.Fatalf("expected completed status, got %s", result.Status)
	}
	if forwarder.releaseCalls != 1 {
		t.Fatalf("expected 1 release call, got %d", forwarder.releaseCalls)
	}
	if forwarder.releasedInst != "inst-3" || forwarder.releasedPort != 20003 {
		t.Fatalf("unexpected release target instance=%s port=%d", forwarder.releasedInst, forwarder.releasedPort)
	}
}
