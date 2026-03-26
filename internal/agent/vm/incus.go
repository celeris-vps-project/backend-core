//go:build linux && incus

package vm

import (
	"backend-core/pkg/contracts"
	"fmt"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/api"
)

// IncusDriver manages KVM VMs and system containers through the official
// Incus Go client library (github.com/lxc/incus/v6/client).
//
// Host requirements:
//   - incus daemon running
//   - /var/lib/incus/unix.socket accessible (or use a remote URL)
type IncusDriver struct {
	client  incus.InstanceServer
	project string
}

// NewIncusDriver connects to the local Incus daemon via its Unix socket.
// socketPath defaults to "" which lets the client use the standard path.
func NewIncusDriver(project, socketPath string) (*IncusDriver, error) {
	if project == "" {
		project = "default"
	}

	c, err := incus.ConnectIncusUnix(socketPath, &incus.ConnectionArgs{})
	if err != nil {
		return nil, fmt.Errorf("incus connect: %w", err)
	}
	c = c.UseProject(project)

	return &IncusDriver{client: c, project: project}, nil
}

func incusName(instanceID string) string {
	return "celeris-" + instanceID
}

func (d *IncusDriver) Create(spec contracts.ProvisionSpec) error {
	name := incusName(spec.InstanceID)

	pool := spec.StoragePool
	if pool == "" {
		pool = "default"
	}
	network := spec.NetworkName
	if network == "" {
		network = "incusbr0"
	}

	instanceType := "container"
	if spec.VirtType == contracts.VirtKVM {
		instanceType = "virtual-machine"
	}

	req := api.InstancesPost{
		Name: name,
		Type: api.InstanceType(instanceType),
		Source: api.InstanceSource{
			Type:  "image",
			Alias: spec.OS,
		},
		InstancePut: api.InstancePut{
			Config: map[string]string{
				"limits.cpu":    fmt.Sprintf("%d", spec.CPU),
				"limits.memory": fmt.Sprintf("%dMiB", spec.MemoryMB),
			},
			Devices: map[string]map[string]string{
				"root": {
					"type": "disk",
					"pool": pool,
					"path": "/",
					"size": fmt.Sprintf("%dGiB", spec.DiskGB),
				},
				"eth0": {
					"type":    "nic",
					"network": network,
					"name":    "eth0",
				},
			},
		},
	}

	// Create and start the instance.
	op, err := d.client.CreateInstance(req)
	if err != nil {
		return fmt.Errorf("incus create %s: %w", name, err)
	}
	if err := op.Wait(); err != nil {
		return fmt.Errorf("incus create wait %s: %w", name, err)
	}

	// Start it.
	startOp, err := d.client.UpdateInstanceState(name, api.InstanceStatePut{
		Action:  "start",
		Timeout: -1,
	}, "")
	if err != nil {
		return fmt.Errorf("incus start %s: %w", name, err)
	}
	return startOp.Wait()
}

func (d *IncusDriver) Start(instanceID string) error {
	name := incusName(instanceID)
	op, err := d.client.UpdateInstanceState(name, api.InstanceStatePut{
		Action:  "start",
		Timeout: -1,
	}, "")
	if err != nil {
		return fmt.Errorf("incus start %s: %w", name, err)
	}
	return op.Wait()
}

func (d *IncusDriver) Stop(instanceID string) error {
	name := incusName(instanceID)
	op, err := d.client.UpdateInstanceState(name, api.InstanceStatePut{
		Action:  "stop",
		Timeout: 30,
	}, "")
	if err != nil {
		return fmt.Errorf("incus stop %s: %w", name, err)
	}
	return op.Wait()
}

func (d *IncusDriver) Reboot(instanceID string) error {
	name := incusName(instanceID)
	op, err := d.client.UpdateInstanceState(name, api.InstanceStatePut{
		Action:  "restart",
		Timeout: 30,
	}, "")
	if err != nil {
		return fmt.Errorf("incus restart %s: %w", name, err)
	}
	return op.Wait()
}

func (d *IncusDriver) Destroy(instanceID string) error {
	name := incusName(instanceID)

	// Force-stop first (ignore error if already stopped).
	stopOp, err := d.client.UpdateInstanceState(name, api.InstanceStatePut{
		Action:  "stop",
		Timeout: 10,
		Force:   true,
	}, "")
	if err == nil {
		_ = stopOp.Wait()
	}

	// Delete the instance.
	delOp, err := d.client.DeleteInstance(name)
	if err != nil {
		return fmt.Errorf("incus delete %s: %w", name, err)
	}
	return delOp.Wait()
}

func (d *IncusDriver) List() ([]*VMInfo, error) {
	instances, err := d.client.GetInstances(api.InstanceTypeAny)
	if err != nil {
		return nil, fmt.Errorf("incus list: %w", err)
	}

	var list []*VMInfo
	for _, inst := range instances {
		// Only include instances managed by Celeris (prefixed with "celeris-")
		if len(inst.Name) <= len("celeris-") {
			continue
		}
		if inst.Name[:8] != "celeris-" {
			continue
		}
		instanceID := inst.Name[8:] // strip "celeris-" prefix

		state := "unknown"
		switch inst.StatusCode {
		case api.Running:
			state = "running"
		case api.Stopped:
			state = "stopped"
		case api.Frozen:
			state = "paused"
		}

		list = append(list, &VMInfo{
			InstanceID: instanceID,
			State:      state,
		})
	}
	return list, nil
}

func (d *IncusDriver) Info(instanceID string) (*VMInfo, error) {
	name := incusName(instanceID)
	inst, _, err := d.client.GetInstance(name)
	if err != nil {
		return nil, fmt.Errorf("incus info %s: %w", name, err)
	}

	state := "unknown"
	switch inst.StatusCode {
	case api.Running:
		state = "running"
	case api.Stopped:
		state = "stopped"
	case api.Frozen:
		state = "paused"
	}

	return &VMInfo{
		InstanceID: instanceID,
		State:      state,
	}, nil
}

func newIncusHypervisor(opts map[string]string) (Hypervisor, error) {
	return NewIncusDriver(opts["project"], opts["socket"])
}
