package e2b

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Label marks the containers of the fake. It is not fettle.computer, so a cleanup of
// computer containers does not remove them.
const Label = "fettle.fake-e2b"

// BridgePort is the guest port that Docker publishes: the agentd WebSocket bridge.
const BridgePort = 7422

// Docker runs each sandbox as a container of its template image with the docker CLI. A
// container idles until envd starts a process in it; a fork commits the disk of its
// parent into an image.
type Docker struct {
	mu     sync.Mutex
	images map[string]string // sandbox ID -> committed image to remove with it
	size   map[string][2]int
}

// NewDocker returns the Docker machines of the fake.
func NewDocker() *Docker {
	return &Docker{images: map[string]string{}, size: map[string][2]int{}}
}

func docker(ctx context.Context, args ...string) (string, error) {
	var out, errb bytes.Buffer
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("docker %s: %w: %s", args[0], err, strings.TrimSpace(errb.String()))
	}
	return strings.TrimSpace(out.String()), nil
}

func name(id string) string { return "fake-e2b-" + id }

// Image implements Machines.
func (d *Docker) Image(ctx context.Context, ref string) error {
	_, err := docker(ctx, "image", "inspect", "--format", "{{.Id}}", ref)
	return err
}

// Create implements Machines.
func (d *Docker) Create(ctx context.Context, id, image string, cpu, memMiB int) error {
	d.mu.Lock()
	d.size[id] = [2]int{cpu, memMiB}
	d.mu.Unlock()
	_, err := docker(ctx, "run", "-d", "--name", name(id), "--label", Label+"=1",
		// agentd needs NET_ADMIN only for its boot step, as in an E2B microVM as root.
		"--cap-add", "NET_ADMIN", "--cpus", strconv.Itoa(cpu), "--memory", strconv.Itoa(memMiB)+"m", "--shm-size", "512m",
		"-p", fmt.Sprintf("127.0.0.1::%d", BridgePort),
		"--entrypoint", "sleep", image, "infinity")
	return err
}

// Start implements Machines.
func (d *Docker) Start(ctx context.Context, id string, cmd []string, env map[string]string) error {
	args := []string{"exec", "-d", "-u", "root"}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		args = append(args, "-e", k+"="+env[k])
	}
	args = append(append(args, name(id)), cmd...)
	_, err := docker(context.WithoutCancel(ctx), args...)
	return err
}

// Pause implements Machines.
func (d *Docker) Pause(ctx context.Context, id string) error {
	_, err := docker(ctx, "pause", name(id))
	return err
}

// Unpause implements Machines.
func (d *Docker) Unpause(ctx context.Context, id string) error {
	_, err := docker(ctx, "unpause", name(id))
	return err
}

// Fork implements Machines.
func (d *Docker) Fork(ctx context.Context, from, to string) error {
	image := "fake-e2b-fork:" + to
	if _, err := docker(ctx, "commit", name(from), image); err != nil {
		return err
	}
	d.mu.Lock()
	d.images[to] = image
	size := d.size[from]
	d.mu.Unlock()
	return d.Create(ctx, to, image, size[0], size[1])
}

// Remove implements Machines.
func (d *Docker) Remove(ctx context.Context, id string) error {
	ctx = context.WithoutCancel(ctx)
	_, err := docker(ctx, "rm", "-f", "-v", name(id))
	d.mu.Lock()
	image := d.images[id]
	delete(d.images, id)
	delete(d.size, id)
	d.mu.Unlock()
	if image != "" {
		_, _ = docker(ctx, "rmi", "-f", image)
	}
	return err
}

// Addr implements Machines.
func (d *Docker) Addr(ctx context.Context, id string, port int) (string, error) {
	if port != BridgePort {
		return "", ErrNoPort
	}
	out, err := docker(ctx, "port", name(id), fmt.Sprintf("%d/tcp", port))
	if err != nil {
		return "", err
	}
	line, _, _ := strings.Cut(out, "\n")
	return strings.TrimSpace(line), nil
}
