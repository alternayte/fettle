package computers

import (
	"context"
	"errors"
)

// Size is a computer size (§10.2).
type Size string

const (
	SizeSmall   Size = "small"
	SizeBrowser Size = "browser"
	SizeDesktop Size = "desktop"
)

// SizeSpec gives the resources of a size.
type SizeSpec struct {
	VCPU    int
	MemMiB  int
	DiskGiB int
}

// Sizes is the size table of §10.2.
var Sizes = map[Size]SizeSpec{
	SizeSmall:   {VCPU: 1, MemMiB: 1024, DiskGiB: 10},
	SizeBrowser: {VCPU: 2, MemMiB: 2048, DiskGiB: 10},
	SizeDesktop: {VCPU: 2, MemMiB: 3072, DiskGiB: 10},
}

// Spec describes a machine to create (Appendix B).
type Spec struct {
	ZoneID  string
	Image   string            // computer image reference or provider template
	Size    Size              // small, browser, desktop
	Env     map[string]string // FETTLE_AGENTD_TOKEN, TLS material, network rules
	DiskGiB int
	// ExtraHosts maps host names to addresses inside the machine (test DNS, D-122).
	ExtraHosts map[string]string
}

// Progress reports a preparation step.
type Progress struct {
	Step    string `json:"step"`
	State   string `json:"state"` // running, pass, fail
	Detail  string `json:"detail,omitempty"`
	Percent int    `json:"percent,omitempty"`
}

// Stats are the resource figures of a machine.
type Stats struct {
	CPUPercent float64 `json:"cpu_percent"`
	MemMiB     int     `json:"mem_mib"`
	DiskMiB    int     `json:"disk_mib"`
}

// Errors of providers.
var (
	ErrGone        = errors.New("the machine no longer exists")
	ErrUnsupported = errors.New("this provider does not support this operation")
)

// Provider creates and finds machines (Appendix B.1). Providers only manage machine
// lifecycle; every other operation goes to agentd (D-04).
type Provider interface {
	Kind() string
	Caps() Caps
	Prepare(ctx context.Context, progress func(Progress)) error
	Create(ctx context.Context, s Spec) (Machine, error)
	Attach(ctx context.Context, ref string) (Machine, error)
}

// Machine is one machine of a provider.
type Machine interface {
	Ref() string
	AgentdURL(ctx context.Context) (string, error)
	Sleep(ctx context.Context) error
	Resume(ctx context.Context) error
	Fork(ctx context.Context) (Machine, error)
	Destroy(ctx context.Context) error
	Stats(ctx context.Context) (Stats, error)
}
