package domain

import "errors"

const (
	RegionStatusActive   = "active"
	RegionStatusInactive = "inactive"
)

// Region represents a geographical location where nodes are deployed.
type Region struct {
	id       string
	code     string // e.g. "DE-fra"
	name     string // e.g. "Frankfurt, Germany"
	flagIcon string // e.g. "🇩🇪" or a URL to a flag image
	status   string // "active" or "inactive"
}

func NewRegion(id, code, name, flagIcon string) (*Region, error) {
	if id == "" {
		return nil, errors.New("domain_error: id is required")
	}
	if code == "" {
		return nil, errors.New("domain_error: code is required")
	}
	if name == "" {
		return nil, errors.New("domain_error: name is required")
	}
	return &Region{
		id:       id,
		code:     code,
		name:     name,
		flagIcon: flagIcon,
		status:   RegionStatusActive,
	}, nil
}

func ReconstituteRegion(id, code, name, flagIcon, status string) *Region {
	return &Region{
		id:       id,
		code:     code,
		name:     name,
		flagIcon: flagIcon,
		status:   status,
	}
}

func (r *Region) ID() string       { return r.id }
func (r *Region) Code() string     { return r.code }
func (r *Region) Name() string     { return r.name }
func (r *Region) FlagIcon() string { return r.flagIcon }
func (r *Region) Status() string   { return r.status }

func (r *Region) Activate()      { r.status = RegionStatusActive }
func (r *Region) Deactivate()    { r.status = RegionStatusInactive }
func (r *Region) IsActive() bool { return r.status == RegionStatusActive }
