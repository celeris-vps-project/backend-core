//go:build linux && libvirt

package vm

import (
	"backend-core/pkg/contracts"
	"fmt"

	libvirt "libvirt.org/go/libvirt"
)

// LibvirtDriver manages KVM virtual machines and LXC containers through
// the official libvirt Go API (libvirt.org/go/libvirt).
//
// Host requirements:
//   - libvirtd running
//   - libvirt development headers installed (for cgo)
type LibvirtDriver struct {
	conn *libvirt.Connect
}

// NewLibvirtDriver opens a persistent connection to the libvirt daemon.
// URI examples:
//   - "qemu:///system"   (local KVM — default)
//   - "lxc:///"          (local LXC)
//   - "qemu+ssh://root@host/system" (remote)
func NewLibvirtDriver(uri string) (*LibvirtDriver, error) {
	if uri == "" {
		uri = "qemu:///system"
	}
	conn, err := libvirt.NewConnect(uri)
	if err != nil {
		return nil, fmt.Errorf("libvirt connect %s: %w", uri, err)
	}
	return &LibvirtDriver{conn: conn}, nil
}

// Close releases the libvirt connection. Call on agent shutdown.
func (d *LibvirtDriver) Close() {
	if d.conn != nil {
		_, _ = d.conn.Close()
	}
}

func domainName(instanceID string) string {
	return "celeris-" + instanceID
}

func (d *LibvirtDriver) Create(spec contracts.ProvisionSpec) error {
	name := domainName(spec.InstanceID)

	virtType := "kvm"
	if spec.VirtType == contracts.VirtLXC {
		virtType = "lxc"
	}

	pool := spec.StoragePool
	if pool == "" {
		pool = "default"
	}
	network := spec.NetworkName
	if network == "" {
		network = "default"
	}

	// Build the domain XML.
	// In production, use a template engine or libvirt-go-xml builder;
	// this inline XML covers the core fields cleanly.
	xml := fmt.Sprintf(`<domain type='%s'>
  <name>%s</name>
  <memory unit='MiB'>%d</memory>
  <vcpu>%d</vcpu>
  <os>
    <type arch='x86_64'>hvm</type>
    <boot dev='hd'/>
  </os>
  <devices>
    <disk type='volume' device='disk'>
      <source pool='%s' volume='%s'/>
      <target dev='vda' bus='virtio'/>
    </disk>
    <interface type='network'>
      <source network='%s'/>
      <model type='virtio'/>
    </interface>
    <console type='pty'/>
  </devices>
</domain>`,
		virtType, name, spec.MemoryMB, spec.CPU,
		pool, name, network,
	)

	// Create the storage volume for the root disk.
	if err := d.createVolume(pool, name, spec.DiskGB); err != nil {
		return fmt.Errorf("libvirt create volume: %w", err)
	}

	// Define the domain from XML.
	dom, err := d.conn.DomainDefineXML(xml)
	if err != nil {
		return fmt.Errorf("libvirt define %s: %w", name, err)
	}
	defer dom.Free()

	// Start the domain immediately.
	if err := dom.Create(); err != nil {
		return fmt.Errorf("libvirt start %s: %w", name, err)
	}
	return nil
}

func (d *LibvirtDriver) Start(instanceID string) error {
	dom, err := d.conn.LookupDomainByName(domainName(instanceID))
	if err != nil {
		return fmt.Errorf("libvirt lookup %s: %w", instanceID, err)
	}
	defer dom.Free()
	return dom.Create()
}

func (d *LibvirtDriver) Stop(instanceID string) error {
	dom, err := d.conn.LookupDomainByName(domainName(instanceID))
	if err != nil {
		return fmt.Errorf("libvirt lookup %s: %w", instanceID, err)
	}
	defer dom.Free()
	return dom.Shutdown()
}

func (d *LibvirtDriver) Reboot(instanceID string) error {
	dom, err := d.conn.LookupDomainByName(domainName(instanceID))
	if err != nil {
		return fmt.Errorf("libvirt lookup %s: %w", instanceID, err)
	}
	defer dom.Free()
	return dom.Reboot(libvirt.DOMAIN_REBOOT_DEFAULT)
}

func (d *LibvirtDriver) Destroy(instanceID string) error {
	name := domainName(instanceID)
	dom, err := d.conn.LookupDomainByName(name)
	if err != nil {
		return fmt.Errorf("libvirt lookup %s: %w", name, err)
	}
	defer dom.Free()

	// Force power-off (ignore error if already off).
	_ = dom.Destroy()

	// Undefine removes the domain definition.
	if err := dom.UndefineFlags(libvirt.DOMAIN_UNDEFINE_MANAGED_SAVE |
		libvirt.DOMAIN_UNDEFINE_SNAPSHOTS_METADATA |
		libvirt.DOMAIN_UNDEFINE_NVRAM); err != nil {
		return fmt.Errorf("libvirt undefine %s: %w", name, err)
	}
	return nil
}

func (d *LibvirtDriver) Info(instanceID string) (*VMInfo, error) {
	name := domainName(instanceID)
	dom, err := d.conn.LookupDomainByName(name)
	if err != nil {
		return nil, fmt.Errorf("libvirt lookup %s: %w", name, err)
	}
	defer dom.Free()

	info, err := dom.GetInfo()
	if err != nil {
		return nil, fmt.Errorf("libvirt info %s: %w", name, err)
	}

	state := "unknown"
	switch libvirt.DomainState(info.State) {
	case libvirt.DOMAIN_RUNNING:
		state = "running"
	case libvirt.DOMAIN_PAUSED:
		state = "paused"
	case libvirt.DOMAIN_SHUTDOWN:
		state = "stopped"
	case libvirt.DOMAIN_SHUTOFF:
		state = "stopped"
	case libvirt.DOMAIN_CRASHED:
		state = "crashed"
	}

	return &VMInfo{
		InstanceID: instanceID,
		State:      state,
		CPU:        int(info.NrVirtCpu),
		MemoryMB:   int(info.MaxMem / 1024),
	}, nil
}

// createVolume creates a storage volume in the given pool.
func (d *LibvirtDriver) createVolume(poolName, volName string, sizeGB int) error {
	pool, err := d.conn.LookupStoragePoolByName(poolName)
	if err != nil {
		return fmt.Errorf("pool %s: %w", poolName, err)
	}
	defer pool.Free()

	xml := fmt.Sprintf(`<volume>
  <name>%s</name>
  <capacity unit='GiB'>%d</capacity>
  <target>
    <format type='qcow2'/>
  </target>
</volume>`, volName, sizeGB)

	vol, err := pool.StorageVolCreateXML(xml, 0)
	if err != nil {
		return err
	}
	vol.Free()
	return nil
}

func newLibvirtHypervisor(opts map[string]string) (Hypervisor, error) {
	return NewLibvirtDriver(opts["uri"])
}
