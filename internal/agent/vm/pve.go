package vm

import (
	"backend-core/pkg/contracts"
	"fmt"
	"log"
	"strings"
	"time"
)

// PVEDriver manages QEMU/KVM virtual machines through the Proxmox VE REST API.
//
// Unlike LibvirtDriver (local C bindings) and IncusDriver (local Unix socket),
// PVEDriver communicates over HTTP(S), making it usable both locally on the
// PVE host and remotely. No build tags or C dependencies are required.
//
// VM naming convention: each Celeris-managed VM has its name set to
// "celeris-<instanceID>" so we can map between Celeris instance IDs and
// PVE's integer VMIDs.
//
// Host requirements:
//   - Proxmox VE 7.x or 8.x with API access enabled
//   - An API token with appropriate permissions (VM.Allocate, VM.PowerMgmt, etc.)
//   - A template VM for cloning (identified by VMID in virt_opts["template_vmid"])
type PVEDriver struct {
	client      *PVEClient
	node        string // PVE node name, e.g. "pve1"
	templateID  int    // default template VMID for cloning
	storagePool string // target storage for cloned disks, e.g. "local-lvm"
}

// PVEDriverConfig holds configuration for creating a PVEDriver.
type PVEDriverConfig struct {
	APIURL         string // e.g. "https://127.0.0.1:8006"
	TokenID        string // e.g. "root@pam!celeris"
	TokenSecret    string
	Node           string // PVE node name
	Insecure       bool   // skip TLS verification
	TemplateVMID   int    // default template VMID for cloning
	StoragePool    string // target storage, e.g. "local-lvm"
	TaskTimeout    time.Duration
}

// NewPVEDriver creates a PVE hypervisor driver from the given options map.
//
// Supported opts keys:
//   - api_url:            PVE API URL (required), e.g. "https://192.168.1.10:8006"
//   - api_token_id:       API token ID (required), e.g. "root@pam!celeris"
//   - api_token_secret:   API token secret (required)
//   - node:               PVE node name (required), e.g. "pve1"
//   - insecure:           "true" to skip TLS verification (default: false)
//   - template_vmid:      default template VMID for clone-based provisioning
//   - storage:            target storage pool, e.g. "local-lvm"
func NewPVEDriver(opts map[string]string) (*PVEDriver, error) {
	apiURL := opts["api_url"]
	tokenID := opts["api_token_id"]
	tokenSecret := opts["api_token_secret"]
	node := opts["node"]

	if node == "" {
		return nil, fmt.Errorf("pve: node is required in virt_opts")
	}

	insecure := strings.EqualFold(opts["insecure"], "true")

	client, err := NewPVEClient(PVEClientConfig{
		APIURL:      apiURL,
		TokenID:     tokenID,
		TokenSecret: tokenSecret,
		Insecure:    insecure,
	})
	if err != nil {
		return nil, fmt.Errorf("pve driver: %w", err)
	}

	templateID := 0
	if tmpl := opts["template_vmid"]; tmpl != "" {
		if _, err := fmt.Sscanf(tmpl, "%d", &templateID); err != nil {
			return nil, fmt.Errorf("pve: invalid template_vmid %q: %w", tmpl, err)
		}
	}

	storagePool := opts["storage"]
	if storagePool == "" {
		storagePool = "local-lvm"
	}

	log.Printf("[pve-driver] connected to %s node=%s template=%d storage=%s insecure=%v",
		apiURL, node, templateID, storagePool, insecure)

	return &PVEDriver{
		client:      client,
		node:        node,
		templateID:  templateID,
		storagePool: storagePool,
	}, nil
}

// pveName returns the Celeris-convention VM name for a given instance ID.
func pveName(instanceID string) string {
	return "celeris-" + instanceID
}

// ── Hypervisor interface implementation ────────────────────────────────

// Create provisions a new QEMU/KVM VM by cloning the configured template.
//
// The flow:
//  1. Determine which template to clone (from spec.OS → template mapping, or default)
//  2. Acquire the next available VMID
//  3. Clone the template
//  4. Reconfigure CPU, memory, disk, network, description
//  5. Start the VM
func (d *PVEDriver) Create(spec contracts.ProvisionSpec) error {
	name := pveName(spec.InstanceID)

	// Resolve template VMID — use OS field as a template VMID hint, or fall back to default.
	templateID := d.resolveTemplate(spec.OS)
	if templateID == 0 {
		return fmt.Errorf("pve create %s: no template configured (set template_vmid in virt_opts or use a numeric OS field)", name)
	}

	// 1. Get next available VMID
	vmid, err := d.client.NextVMID()
	if err != nil {
		return fmt.Errorf("pve create %s: %w", name, err)
	}

	log.Printf("[pve-driver] CREATE %s: cloning template %d → vmid %d", name, templateID, vmid)

	// 2. Clone the template
	cloneParams := map[string]string{
		"name":    name,
		"full":    "1", // full clone (not linked)
		"storage": d.storagePool,
	}
	// Set description for reverse lookup (instanceID → VMID)
	cloneParams["description"] = fmt.Sprintf("celeris-managed\ninstance_id=%s", spec.InstanceID)

	upid, err := d.client.CloneQEMU(d.node, templateID, vmid, cloneParams)
	if err != nil {
		return fmt.Errorf("pve create %s clone: %w", name, err)
	}

	// Wait for clone to complete (can take a while for large disks)
	if err := d.client.WaitForTask(d.node, upid, 5*time.Minute); err != nil {
		return fmt.Errorf("pve create %s clone wait: %w", name, err)
	}

	// 3. Reconfigure the cloned VM
	configParams := map[string]string{
		"cores":  fmt.Sprintf("%d", spec.CPU),
		"memory": fmt.Sprintf("%d", spec.MemoryMB),
	}
	// Set hostname via cloud-init if available
	if spec.Hostname != "" {
		configParams["name"] = name // PVE display name
	}
	// Add SSH keys via cloud-init (if provided and cloud-init is configured in template)
	if len(spec.SSHKeys) > 0 {
		configParams["sshkeys"] = strings.Join(spec.SSHKeys, "\n")
	}
	// Set network bridge if specified
	if spec.NetworkName != "" {
		configParams["net0"] = fmt.Sprintf("virtio,bridge=%s", spec.NetworkName)
	}
	// Set static IP via cloud-init if provided
	if spec.IPv4 != "" {
		configParams["ipconfig0"] = fmt.Sprintf("ip=%s/24,gw=%s", spec.IPv4, defaultGateway(spec.IPv4))
	}

	if err := d.client.SetQEMUConfig(d.node, vmid, configParams); err != nil {
		log.Printf("[pve-driver] WARNING: failed to reconfigure %s (vmid=%d): %v", name, vmid, err)
		// Non-fatal: VM is cloned but may have template's default config
	}

	// 4. Resize disk if spec.DiskGB is set and larger than template
	if spec.DiskGB > 0 {
		if err := d.client.ResizeQEMUDisk(d.node, vmid, "scsi0", fmt.Sprintf("%dG", spec.DiskGB)); err != nil {
			log.Printf("[pve-driver] WARNING: disk resize failed for %s: %v (trying virtio0)", name, err)
			// Try virtio0 as fallback (different disk bus)
			if err2 := d.client.ResizeQEMUDisk(d.node, vmid, "virtio0", fmt.Sprintf("%dG", spec.DiskGB)); err2 != nil {
				log.Printf("[pve-driver] WARNING: disk resize virtio0 also failed for %s: %v", name, err2)
			}
		}
	}

	// 5. Start the VM
	startUpid, err := d.client.StartQEMU(d.node, vmid)
	if err != nil {
		return fmt.Errorf("pve create %s start: %w", name, err)
	}
	if err := d.client.WaitForTask(d.node, startUpid, 60*time.Second); err != nil {
		return fmt.Errorf("pve create %s start wait: %w", name, err)
	}

	log.Printf("[pve-driver] CREATE %s: done (vmid=%d cpu=%d mem=%dMB disk=%dGB)",
		name, vmid, spec.CPU, spec.MemoryMB, spec.DiskGB)
	return nil
}

// Start boots an existing stopped VM.
func (d *PVEDriver) Start(instanceID string) error {
	name := pveName(instanceID)
	vmid, err := d.findVMID(instanceID)
	if err != nil {
		return fmt.Errorf("pve start %s: %w", name, err)
	}

	upid, err := d.client.StartQEMU(d.node, vmid)
	if err != nil {
		return fmt.Errorf("pve start %s (vmid=%d): %w", name, vmid, err)
	}

	if err := d.client.WaitForTask(d.node, upid, 60*time.Second); err != nil {
		return fmt.Errorf("pve start %s wait: %w", name, err)
	}

	log.Printf("[pve-driver] START %s (vmid=%d)", name, vmid)
	return nil
}

// Stop gracefully shuts down a running VM via ACPI, then force-stops if needed.
func (d *PVEDriver) Stop(instanceID string) error {
	name := pveName(instanceID)
	vmid, err := d.findVMID(instanceID)
	if err != nil {
		return fmt.Errorf("pve stop %s: %w", name, err)
	}

	// Try graceful shutdown first (ACPI)
	upid, err := d.client.ShutdownQEMU(d.node, vmid, 30)
	if err != nil {
		// Fall back to hard stop
		log.Printf("[pve-driver] graceful shutdown failed for %s, forcing stop: %v", name, err)
		upid, err = d.client.StopQEMU(d.node, vmid)
		if err != nil {
			return fmt.Errorf("pve stop %s (vmid=%d): %w", name, vmid, err)
		}
	}

	if err := d.client.WaitForTask(d.node, upid, 60*time.Second); err != nil {
		return fmt.Errorf("pve stop %s wait: %w", name, err)
	}

	log.Printf("[pve-driver] STOP %s (vmid=%d)", name, vmid)
	return nil
}

// Reboot cycles a running VM.
func (d *PVEDriver) Reboot(instanceID string) error {
	name := pveName(instanceID)
	vmid, err := d.findVMID(instanceID)
	if err != nil {
		return fmt.Errorf("pve reboot %s: %w", name, err)
	}

	upid, err := d.client.RebootQEMU(d.node, vmid, 30)
	if err != nil {
		return fmt.Errorf("pve reboot %s (vmid=%d): %w", name, vmid, err)
	}

	if err := d.client.WaitForTask(d.node, upid, 60*time.Second); err != nil {
		return fmt.Errorf("pve reboot %s wait: %w", name, err)
	}

	log.Printf("[pve-driver] REBOOT %s (vmid=%d)", name, vmid)
	return nil
}

// Destroy permanently removes a VM and its storage.
func (d *PVEDriver) Destroy(instanceID string) error {
	name := pveName(instanceID)
	vmid, err := d.findVMID(instanceID)
	if err != nil {
		return fmt.Errorf("pve destroy %s: %w", name, err)
	}

	// Force-stop first (ignore error if already stopped)
	stopUpid, stopErr := d.client.StopQEMU(d.node, vmid)
	if stopErr == nil {
		_ = d.client.WaitForTask(d.node, stopUpid, 30*time.Second)
	}

	// Delete the VM (purge all related data)
	upid, err := d.client.DeleteQEMU(d.node, vmid, map[string]string{
		"purge":          "1",
		"destroy-unreferenced-disks": "1",
	})
	if err != nil {
		return fmt.Errorf("pve destroy %s (vmid=%d): %w", name, vmid, err)
	}

	if err := d.client.WaitForTask(d.node, upid, 120*time.Second); err != nil {
		return fmt.Errorf("pve destroy %s wait: %w", name, err)
	}

	log.Printf("[pve-driver] DESTROY %s (vmid=%d)", name, vmid)
	return nil
}

// Info returns the current runtime state of a VM.
func (d *PVEDriver) Info(instanceID string) (*VMInfo, error) {
	name := pveName(instanceID)
	vmid, err := d.findVMID(instanceID)
	if err != nil {
		return nil, fmt.Errorf("pve info %s: %w", name, err)
	}

	status, err := d.client.GetQEMUStatus(d.node, vmid)
	if err != nil {
		return nil, fmt.Errorf("pve info %s (vmid=%d): %w", name, vmid, err)
	}

	info := &VMInfo{
		InstanceID: instanceID,
		State:      normalizePVEState(status.Status),
		CPU:        status.CPUs,
		MemoryMB:   int(status.MaxMem / (1024 * 1024)),
		DiskGB:     int(status.MaxDisk / (1024 * 1024 * 1024)),
	}

	// Try to get IP from QEMU guest agent (best effort)
	if status.Status == "running" {
		ipv4, ipv6 := d.getVMIPs(vmid)
		info.IPv4 = ipv4
		info.IPv6 = ipv6
	}

	return info, nil
}

// List returns the runtime state of all Celeris-managed VMs on this node.
func (d *PVEDriver) List() ([]*VMInfo, error) {
	vms, err := d.client.ListQEMU(d.node)
	if err != nil {
		return nil, fmt.Errorf("pve list: %w", err)
	}

	var list []*VMInfo
	for _, vm := range vms {
		// Only include VMs managed by Celeris (name starts with "celeris-")
		if !strings.HasPrefix(vm.Name, "celeris-") {
			continue
		}
		instanceID := strings.TrimPrefix(vm.Name, "celeris-")

		list = append(list, &VMInfo{
			InstanceID: instanceID,
			State:      normalizePVEState(vm.Status),
			CPU:        vm.CPUs,
			MemoryMB:   int(vm.MaxMem / (1024 * 1024)),
			DiskGB:     int(vm.MaxDisk / (1024 * 1024 * 1024)),
		})
	}
	return list, nil
}

// ── Internal helpers ───────────────────────────────────────────────────

// findVMID resolves a Celeris instanceID to a PVE integer VMID by
// scanning the VM list for a matching name ("celeris-<instanceID>").
func (d *PVEDriver) findVMID(instanceID string) (int, error) {
	targetName := pveName(instanceID)

	vms, err := d.client.ListQEMU(d.node)
	if err != nil {
		return 0, fmt.Errorf("find vmid: %w", err)
	}

	for _, vm := range vms {
		if vm.Name == targetName {
			return vm.VMID, nil
		}
	}

	return 0, fmt.Errorf("pve: VM %q not found on node %s", targetName, d.node)
}

// resolveTemplate determines the template VMID to use.
// If spec.OS is a numeric string, use it directly as a VMID.
// Otherwise fall back to the configured default template.
func (d *PVEDriver) resolveTemplate(os string) int {
	if os != "" {
		var vmid int
		if _, err := fmt.Sscanf(os, "%d", &vmid); err == nil && vmid > 0 {
			return vmid
		}
	}
	return d.templateID
}

// getVMIPs attempts to retrieve IP addresses from the QEMU guest agent.
// Returns empty strings if the guest agent is not available.
func (d *PVEDriver) getVMIPs(vmid int) (ipv4, ipv6 string) {
	ifaces, err := d.client.GetQEMUAgentNetworkInterfaces(d.node, vmid)
	if err != nil {
		return "", ""
	}

	for _, iface := range ifaces {
		// Skip loopback
		if iface.Name == "lo" {
			continue
		}
		for _, addr := range iface.IPAddresses {
			if addr.Type == "ipv4" && ipv4 == "" && !strings.HasPrefix(addr.Address, "127.") {
				ipv4 = addr.Address
			}
			if addr.Type == "ipv6" && ipv6 == "" && !strings.HasPrefix(addr.Address, "fe80:") && addr.Address != "::1" {
				ipv6 = addr.Address
			}
		}
	}
	return ipv4, ipv6
}

// normalizePVEState converts PVE's status string to Celeris's standard states.
func normalizePVEState(pveStatus string) string {
	switch pveStatus {
	case "running":
		return "running"
	case "stopped":
		return "stopped"
	case "paused":
		return "paused"
	default:
		return "unknown"
	}
}

// defaultGateway guesses the gateway from an IPv4 address by replacing
// the last octet with ".1". This is a simplistic heuristic for MVP;
// production should use explicit gateway configuration.
func defaultGateway(ipv4 string) string {
	parts := strings.Split(ipv4, ".")
	if len(parts) != 4 {
		return ""
	}
	parts[3] = "1"
	return strings.Join(parts, ".")
}
