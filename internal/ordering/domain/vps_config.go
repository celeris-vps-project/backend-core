package domain

import "errors"

// VPSConfig describes the specification of a VPS product being ordered.
type VPSConfig struct {
	hostname string
	plan     string // e.g. "vps-starter", "vps-pro"
	region   string // e.g. "us-east-1"
	os       string // e.g. "ubuntu-22.04"
	cpu      int    // vCPU count
	memoryMB int    // memory in MB
	diskGB   int    // disk in GB
}

func NewVPSConfig(hostname, plan, region, os string, cpu, memoryMB, diskGB int) (VPSConfig, error) {
	if hostname == "" {
		return VPSConfig{}, errors.New("domain_error: hostname is required")
	}
	if plan == "" {
		return VPSConfig{}, errors.New("domain_error: plan is required")
	}
	if region == "" {
		return VPSConfig{}, errors.New("domain_error: region is required")
	}
	if os == "" {
		return VPSConfig{}, errors.New("domain_error: os is required")
	}
	if cpu <= 0 {
		return VPSConfig{}, errors.New("domain_error: cpu must be > 0")
	}
	if memoryMB <= 0 {
		return VPSConfig{}, errors.New("domain_error: memory must be > 0")
	}
	if diskGB <= 0 {
		return VPSConfig{}, errors.New("domain_error: disk must be > 0")
	}
	return VPSConfig{
		hostname: hostname,
		plan:     plan,
		region:   region,
		os:       os,
		cpu:      cpu,
		memoryMB: memoryMB,
		diskGB:   diskGB,
	}, nil
}

func (v VPSConfig) Hostname() string { return v.hostname }
func (v VPSConfig) Plan() string     { return v.plan }
func (v VPSConfig) Region() string   { return v.region }
func (v VPSConfig) OS() string       { return v.os }
func (v VPSConfig) CPU() int         { return v.cpu }
func (v VPSConfig) MemoryMB() int    { return v.memoryMB }
func (v VPSConfig) DiskGB() int      { return v.diskGB }
