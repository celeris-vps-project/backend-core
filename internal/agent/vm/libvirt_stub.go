//go:build !(linux && libvirt)

package vm

import "fmt"

func newLibvirtHypervisor(_ map[string]string) (Hypervisor, error) {
	return nil, fmt.Errorf("libvirt driver not available: rebuild with `-tags libvirt` on a Linux host with libvirt-dev installed")
}
