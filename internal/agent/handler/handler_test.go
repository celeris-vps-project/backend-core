package handler

import (
	"backend-core/internal/agent/vm"
	"backend-core/pkg/contracts"
	"errors"
	"testing"
	"time"
)

type fakeDriver struct {
	createdSpec contracts.ProvisionSpec
	waitInfo    *vm.VMInfo
	waitErr     error
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
	releasedPort int
	releasedInst string
	err          error
	releaseErr   error
	calls        int
	releaseCalls int
}

func (f *fakeForwarder) EnsureForward(instanceID string, hostPort int, guestIP string) error {
	f.calls++
	f.hostPort = hostPort
	f.guestIP = guestIP
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
	if forwarder.hostPort != 20001 || forwarder.guestIP != "10.0.0.15" {
		t.Fatalf("unexpected NAT forward target: port=%d ip=%s", forwarder.hostPort, forwarder.guestIP)
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
