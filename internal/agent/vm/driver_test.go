package vm

import (
	"backend-core/pkg/contracts"
	"testing"
)

// Verify StubDriver satisfies Hypervisor at compile time.
var _ Hypervisor = (*StubDriver)(nil)

func TestStubDriver_FullLifecycle(t *testing.T) {
	d := NewStubDriver()

	spec := contracts.ProvisionSpec{
		InstanceID:  "test-01",
		Hostname:    "web-01",
		OS:          "ubuntu-22.04",
		CPU:         2,
		MemoryMB:    2048,
		DiskGB:      40,
		VirtType:    contracts.VirtKVM,
		StoragePool: "default",
		NetworkName: "br0",
	}

	// Create → state should be "stopped"
	if err := d.Create(spec); err != nil {
		t.Fatalf("Create: %v", err)
	}
	info, err := d.Info("test-01")
	if err != nil {
		t.Fatalf("Info after create: %v", err)
	}
	if info.State != "stopped" {
		t.Fatalf("expected stopped after create, got %s", info.State)
	}
	if info.CPU != 2 || info.MemoryMB != 2048 || info.DiskGB != 40 {
		t.Fatalf("spec mismatch: %+v", info)
	}

	// Start → running
	if err := d.Start("test-01"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	info, _ = d.Info("test-01")
	if info.State != "running" {
		t.Fatalf("expected running, got %s", info.State)
	}

	// Start again → error
	if err := d.Start("test-01"); err == nil {
		t.Fatal("expected error starting already-running instance")
	}

	// Reboot → still running
	if err := d.Reboot("test-01"); err != nil {
		t.Fatalf("Reboot: %v", err)
	}
	info, _ = d.Info("test-01")
	if info.State != "running" {
		t.Fatalf("expected running after reboot, got %s", info.State)
	}

	// Stop → stopped
	if err := d.Stop("test-01"); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	info, _ = d.Info("test-01")
	if info.State != "stopped" {
		t.Fatalf("expected stopped, got %s", info.State)
	}

	// Stop again → error
	if err := d.Stop("test-01"); err == nil {
		t.Fatal("expected error stopping already-stopped instance")
	}

	// Destroy
	if err := d.Destroy("test-01"); err != nil {
		t.Fatalf("Destroy: %v", err)
	}

	// Info after destroy → error
	if _, err := d.Info("test-01"); err == nil {
		t.Fatal("expected error querying destroyed instance")
	}
}

func TestStubDriver_DuplicateCreate(t *testing.T) {
	d := NewStubDriver()
	spec := contracts.ProvisionSpec{InstanceID: "dup-1", Hostname: "h", OS: "debian", CPU: 1, MemoryMB: 512, DiskGB: 10, VirtType: contracts.VirtKVM}

	if err := d.Create(spec); err != nil {
		t.Fatalf("first create: %v", err)
	}
	if err := d.Create(spec); err == nil {
		t.Fatal("expected error on duplicate create")
	}
}

func TestStubDriver_NotFound(t *testing.T) {
	d := NewStubDriver()

	if err := d.Start("nonexistent"); err == nil {
		t.Fatal("expected error starting nonexistent")
	}
	if err := d.Stop("nonexistent"); err == nil {
		t.Fatal("expected error stopping nonexistent")
	}
	if err := d.Destroy("nonexistent"); err == nil {
		t.Fatal("expected error destroying nonexistent")
	}
	if _, err := d.Info("nonexistent"); err == nil {
		t.Fatal("expected error info nonexistent")
	}
}

func TestFactory_Stub(t *testing.T) {
	h, err := NewHypervisor(BackendStub, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := h.(*StubDriver); !ok {
		t.Fatalf("expected StubDriver, got %T", h)
	}
}

func TestFactory_EmptyBackend(t *testing.T) {
	h, err := NewHypervisor("", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := h.(*StubDriver); !ok {
		t.Fatalf("expected StubDriver for empty backend, got %T", h)
	}
}

func TestFactory_Unknown(t *testing.T) {
	_, err := NewHypervisor("proxmox", nil)
	if err == nil {
		t.Fatal("expected error for unknown backend")
	}
}

func TestFactory_LibvirtFallback(t *testing.T) {
	// Without the "libvirt" build tag, this should return a descriptive error.
	_, err := NewHypervisor(BackendLibvirt, map[string]string{"uri": "qemu:///system"})
	if err == nil {
		// If it succeeds, we're on a Linux host with libvirt — that's fine too.
		return
	}
	if err.Error() == "" {
		t.Fatal("expected non-empty error message")
	}
}

func TestFactory_IncusFallback(t *testing.T) {
	// Without the "incus" build tag, this should return a descriptive error.
	_, err := NewHypervisor(BackendIncus, map[string]string{"project": "default"})
	if err == nil {
		return
	}
	if err.Error() == "" {
		t.Fatal("expected non-empty error message")
	}
}

func TestExecute_AllTaskTypes(t *testing.T) {
	d := NewStubDriver()
	spec := contracts.ProvisionSpec{InstanceID: "x-1", Hostname: "h", OS: "debian", CPU: 1, MemoryMB: 512, DiskGB: 10, VirtType: contracts.VirtKVM}

	// Pre-create the instance so lifecycle calls succeed.
	if err := d.Create(spec); err != nil {
		t.Fatalf("setup create: %v", err)
	}

	// Provision again with a different ID to test the provision task type.
	provSpec := spec
	provSpec.InstanceID = "x-2"

	provTask := contracts.Task{ID: "t-prov", Type: contracts.TaskProvision, Spec: provSpec}
	if err := Execute(d, provTask); err != nil {
		t.Fatalf("Execute provision: %v", err)
	}

	// Now test lifecycle on x-1 (already created, state=stopped).
	lifecycleTypes := []contracts.TaskType{
		contracts.TaskStart,     // stopped → running
		contracts.TaskReboot,    // running → running
		contracts.TaskStop,      // running → stopped
		contracts.TaskUnsuspend, // stopped → running (alias for start)
		contracts.TaskSuspend,   // running → stopped (alias for stop)
	}
	for _, tt := range lifecycleTypes {
		task := contracts.Task{ID: "t-1", Type: tt, Spec: spec}
		if err := Execute(d, task); err != nil {
			t.Fatalf("Execute %s: %v", tt, err)
		}
	}

	// Deprovision
	depTask := contracts.Task{ID: "t-dep", Type: contracts.TaskDeprovision, Spec: spec}
	if err := Execute(d, depTask); err != nil {
		t.Fatalf("Execute deprovision: %v", err)
	}

	// Unknown type should error
	unknownTask := contracts.Task{ID: "t-unk", Type: "nuke", Spec: spec}
	if err := Execute(d, unknownTask); err == nil {
		t.Fatal("expected error for unknown type")
	}
}

func TestStubDriver_LXCSpec(t *testing.T) {
	d := NewStubDriver()
	spec := contracts.ProvisionSpec{
		InstanceID: "lxc-01",
		Hostname:   "ct-01",
		OS:         "alpine-3.19",
		CPU:        1,
		MemoryMB:   512,
		DiskGB:     10,
		VirtType:   contracts.VirtLXC,
	}
	if err := d.Create(spec); err != nil {
		t.Fatalf("Create LXC: %v", err)
	}
	info, _ := d.Info("lxc-01")
	if info.State != "stopped" {
		t.Fatalf("expected stopped, got %s", info.State)
	}
}
