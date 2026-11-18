package computers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"slices"
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
	// TotalMB is the size of a download in MB, when the provider knows it.
	TotalMB int `json:"total_mb,omitempty"`
}

// DevImage is the computer image of a build without a release image: `just image` builds it
// in the local Docker.
const DevImage = "fettle-computer:dev"

// ImageHint is the next step after a failed image download.
const ImageHint = "Check the internet connection, or set another image in Settings, Computers"

// LocalBuildHint is the next step when the local build of the image is missing.
const LocalBuildHint = "Run `just image`"

// ImageError is the error of an image that Fettle could not get. It names the image and the
// next step; a local build gets the command that builds it.
func ImageError(image string, err error) error {
	if image == DevImage {
		return fmt.Errorf("the image %s is a local build and is not here: %w. %s", image, err, LocalBuildHint)
	}
	return fmt.Errorf("could not get the computer image %s: %w. %s", image, err, ImageHint)
}

// ImageHash is a short hash of an image reference. Template names of remote providers hold
// it, so a new image builds new templates.
func ImageHash(image string) string {
	sum := sha256.Sum256([]byte(image))
	return hex.EncodeToString(sum[:4])
}

// DownloadDetail is the detail of the image step while it downloads.
func DownloadDetail(totalBytes int64) string {
	if totalBytes <= 0 {
		return "Downloading the computer image"
	}
	return fmt.Sprintf("Downloading the computer image (%d MB)", totalBytes>>20)
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

// Dialer is a machine whose agentd port is reached through the provider's API, not a
// network address (kiln streams). The client runs its pinned TLS inside the connection.
type Dialer interface {
	DialAgentd(ctx context.Context, network, addr string) (net.Conn, error)
}

// DialOf returns the dial function of a machine, or nil for a plain network address.
func DialOf(m Machine) func(ctx context.Context, network, addr string) (net.Conn, error) {
	if d, ok := m.(Dialer); ok {
		return d.DialAgentd
	}
	return nil
}

// EgressHosts returns the hosts that the edge of a remote provider must allow for a computer
// with the agentd env env: the hosts of an allowlist zone, plus the host of the upstream
// proxy when one is set, because the filter proxy in the computer reaches the sites through
// it. ok is false for an open zone.
func EgressHosts(env map[string]string) (hosts []string, ok bool) {
	var n struct {
		Mode       string   `json:"mode"`
		AllowHosts []string `json:"allow_hosts"`
	}
	if err := json.Unmarshal([]byte(env["FETTLE_AGENTD_NETWORK"]), &n); err != nil || n.Mode != "allowlist" {
		return nil, false
	}
	hosts = append([]string{}, n.AllowHosts...)
	if raw := env["FETTLE_AGENTD_PROXY"]; raw != "" {
		if u, err := url.Parse(raw); err == nil && u.Hostname() != "" && !slices.Contains(hosts, u.Hostname()) {
			hosts = append(hosts, u.Hostname())
		}
	}
	return hosts, true
}
