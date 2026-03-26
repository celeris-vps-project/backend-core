package vm

import "fmt"

// Backend selects which Hypervisor implementation to use.
type Backend string

const (
	BackendStub    Backend = "stub"
	BackendLibvirt Backend = "libvirt"
	BackendIncus   Backend = "incus"
	BackendPVE     Backend = "pve"
)

// NewHypervisor is the factory that returns the correct Hypervisor
// implementation based on the configured backend.
//
//   - "libvirt" → LibvirtDriver  (KVM/LXC via libvirt Go API, requires -tags libvirt on Linux)
//   - "incus"   → IncusDriver   (KVM/LXC via Incus Go client)
//   - "pve"     → PVEDriver     (QEMU/KVM via Proxmox VE REST API, no build tags needed)
//   - "stub"    → StubDriver    (in-memory, for dev/test)
//
// opts keys:
//
//	libvirt: "uri"              – e.g. "qemu:///system"
//	incus:   "project"          – e.g. "default"
//	         "socket"           – e.g. "/var/lib/incus/unix.socket"
//	pve:     "api_url"          – e.g. "https://192.168.1.10:8006"
//	         "api_token_id"     – e.g. "root@pam!celeris"
//	         "api_token_secret" – API token secret
//	         "node"             – PVE node name, e.g. "pve1"
//	         "insecure"         – "true" to skip TLS verify
//	         "template_vmid"   – default template VMID for cloning
//	         "storage"          – target storage pool, e.g. "local-lvm"
func NewHypervisor(backend Backend, opts map[string]string) (Hypervisor, error) {
	if opts == nil {
		opts = map[string]string{}
	}
	switch backend {
	case BackendLibvirt:
		return newLibvirtHypervisor(opts)
	case BackendIncus:
		return newIncusHypervisor(opts)
	case BackendPVE:
		return NewPVEDriver(opts)
	case BackendStub, "":
		return NewStubDriver(), nil
	default:
		return nil, fmt.Errorf("unknown vm backend: %q (supported: libvirt, incus, pve, stub)", backend)
	}
}
