package vm

import (
	"backend-core/pkg/contracts"
	"fmt"
	"log"
	"sync"
)

// stubInstance tracks a simulated guest in memory.
type stubInstance struct {
	Spec  contracts.ProvisionSpec
	State string // "stopped", "running"
}

// StubDriver is an in-memory Hypervisor that simulates VM state.
// Use for development, testing, and CI — no host dependencies.
type StubDriver struct {
	mu        sync.Mutex
	instances map[string]*stubInstance
}

func NewStubDriver() *StubDriver {
	return &StubDriver{instances: make(map[string]*stubInstance)}
}

func (d *StubDriver) Create(spec contracts.ProvisionSpec) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	name := spec.InstanceID
	if _, exists := d.instances[name]; exists {
		return fmt.Errorf("stub: instance %s already exists", name)
	}
	d.instances[name] = &stubInstance{Spec: spec, State: "stopped"}
	log.Printf("[vm-stub] CREATE instance=%s virt=%s cpu=%d mem=%dMB disk=%dGB os=%s",
		name, spec.VirtType, spec.CPU, spec.MemoryMB, spec.DiskGB, spec.OS)
	return nil
}

func (d *StubDriver) Start(instanceID string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	inst, ok := d.instances[instanceID]
	if !ok {
		return fmt.Errorf("stub: instance %s not found", instanceID)
	}
	if inst.State == "running" {
		return fmt.Errorf("stub: instance %s already running", instanceID)
	}
	inst.State = "running"
	log.Printf("[vm-stub] START instance=%s", instanceID)
	return nil
}

func (d *StubDriver) Stop(instanceID string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	inst, ok := d.instances[instanceID]
	if !ok {
		return fmt.Errorf("stub: instance %s not found", instanceID)
	}
	if inst.State == "stopped" {
		return fmt.Errorf("stub: instance %s already stopped", instanceID)
	}
	inst.State = "stopped"
	log.Printf("[vm-stub] STOP instance=%s", instanceID)
	return nil
}

func (d *StubDriver) Reboot(instanceID string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	inst, ok := d.instances[instanceID]
	if !ok {
		return fmt.Errorf("stub: instance %s not found", instanceID)
	}
	inst.State = "running"
	log.Printf("[vm-stub] REBOOT instance=%s", instanceID)
	return nil
}

func (d *StubDriver) Destroy(instanceID string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if _, ok := d.instances[instanceID]; !ok {
		return fmt.Errorf("stub: instance %s not found", instanceID)
	}
	delete(d.instances, instanceID)
	log.Printf("[vm-stub] DESTROY instance=%s", instanceID)
	return nil
}

func (d *StubDriver) Info(instanceID string) (*VMInfo, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	inst, ok := d.instances[instanceID]
	if !ok {
		return nil, fmt.Errorf("stub: instance %s not found", instanceID)
	}
	return &VMInfo{
		InstanceID: instanceID,
		State:      inst.State,
		CPU:        inst.Spec.CPU,
		MemoryMB:   inst.Spec.MemoryMB,
		DiskGB:     inst.Spec.DiskGB,
	}, nil
}

func (d *StubDriver) List() ([]*VMInfo, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	var list []*VMInfo
	for id, inst := range d.instances {
		list = append(list, &VMInfo{
			InstanceID: id,
			State:      inst.State,
			CPU:        inst.Spec.CPU,
			MemoryMB:   inst.Spec.MemoryMB,
			DiskGB:     inst.Spec.DiskGB,
		})
	}
	return list, nil
}
