//go:build !(linux && incus)

package vm

import "fmt"

func newIncusHypervisor(_ map[string]string) (Hypervisor, error) {
	return nil, fmt.Errorf("incus driver not available: rebuild with `-tags incus` on a Linux host with the Incus daemon running")
}
