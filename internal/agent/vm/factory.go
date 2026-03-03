package vm

import "fmt"

// Backend selects which Hypervisor implementation to use.
type Backend string

const (
	BackendStub    Backend = "stub"
	BackendLibvirt Backend = "libvirt"
	BackendIncus   Backend = "incus"
)

// NewHypervisor is the factory that returns the correct Hypervisor
// implementation based on the configured backend.
//
//   - "libvirt" → LibvirtDriver  (KVM/LXC via libvirt Go API, requires -tags libvirt on Linux)
//   - "incus"   → IncusDriver   (KVM/LXC via Incus Go client)
//   - "stub"    → StubDriver    (in-memory, for dev/test)
//
// opts keys:
//
//	libvirt: "uri"     – e.g. "qemu:///system"
//	incus:   "project" – e.g. "default"
//	         "socket"  – e.g. "/var/lib/incus/unix.socket"
func NewHypervisor(backend Backend, opts map[string]string) (Hypervisor, error) {
	if opts == nil {
		opts = map[string]string{}
	}
	switch backend {
	case BackendLibvirt:
		return newLibvirtHypervisor(opts)
	case BackendIncus:
		return newIncusHypervisor(opts)
	case BackendStub, "":
		return NewStubDriver(), nil
	default:
		return nil, fmt.Errorf("unknown vm backend: %q (supported: libvirt, incus, stub)", backend)
	}
}
