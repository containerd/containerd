package runm

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"

	"github.com/containerd/containerd/v2/api/runtime/task/v3"
	"github.com/containerd/containerd/v2/cmd/containerd-shim-runc-v2/process"
	runm_process "github.com/containerd/containerd/v2/cmd/containerd-shim-runc-v2/runm/process"
	"github.com/containerd/containerd/v2/pkg/stdio"
	"github.com/containerd/errdefs"
)

// Container for operating on a runc container and its processes
type Container struct {
	mu sync.Mutex

	// ID of the container
	ID string
	// Bundle path
	Bundle string

	process process.Process
}

// NewContainer returns a new runc container
func NewContainer(ctx context.Context, r *task.CreateTaskRequest) (_ *Container, retErr error) {
	var pmounts []process.Mount
	for _, m := range r.Rootfs {
		pmounts = append(pmounts, process.Mount{
			Type:    m.Type,
			Source:  m.Source,
			Target:  m.Target,
			Options: m.Options,
		})
	}

	config := &process.CreateConfig{
		ID:               r.ID,
		Bundle:           r.Bundle,
		Rootfs:           pmounts,
		Terminal:         r.Terminal,
		Stdin:            r.Stdin,
		Stdout:           r.Stdout,
		Stderr:           r.Stderr,
		Checkpoint:       r.Checkpoint,
		ParentCheckpoint: r.ParentCheckpoint,
		Options:          r.Options,
	}

	rootfs := r.Rootfs[0].Source

	p, err := newInit(
		ctx,
		r.Bundle,
		filepath.Join(r.Bundle, "work"),
		config,
		rootfs,
	)
	if err != nil {
		return nil, errdefs.ToGRPC(err)
	}
	if err := p.Create(ctx, config); err != nil {
		return nil, errdefs.ToGRPC(err)
	}

	container := &Container{
		ID:      r.ID,
		Bundle:  r.Bundle,
		process: p,
	}
	return container, nil
}

func newInit(ctx context.Context, path, workDir string, r *process.CreateConfig, rootfs string) (*runm_process.Init, error) {
	p := runm_process.New(r.ID, stdio.Stdio{
		Stdin:    r.Stdin,
		Stdout:   r.Stdout,
		Stderr:   r.Stderr,
		Terminal: r.Terminal,
	})
	p.Bundle = r.Bundle
	p.Rootfs = rootfs
	p.WorkDir = workDir
	return p, nil
}

// Start a container process
func (c *Container) Start(ctx context.Context, r *task.StartRequest) (process.Process, error) {
	p, err := c.Process(r.ExecID)
	if err != nil {
		return nil, err
	}
	if err := p.Start(ctx); err != nil {
		return p, err
	}
	return p, nil
}

// Delete the container or a process by id
func (c *Container) Delete(ctx context.Context, r *task.DeleteRequest) (process.Process, error) {
	p, err := c.Process(r.ExecID)
	if err != nil {
		return nil, err
	}
	if err := p.Delete(ctx); err != nil {
		return nil, err
	}
	return p, nil
}

// Kill a process
func (c *Container) Kill(ctx context.Context, r *task.KillRequest) error {
	p, err := c.Process(r.ExecID)
	if err != nil {
		return err
	}
	return p.Kill(ctx, r.Signal, r.All)
}

// Update the resource information of a running container
func (c *Container) Update(ctx context.Context, r *task.UpdateTaskRequest) error {
	return nil
}

// Process returns the process by id
func (c *Container) Process(id string) (process.Process, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if id == "" {
		if c.process == nil {
			return nil, fmt.Errorf("container must be created: %w", errdefs.ErrFailedPrecondition)
		}
		return c.process, nil
	}
	return nil, fmt.Errorf("process does not exist %s: %w", id, errdefs.ErrNotFound)
}

// Pid of the main process of a container
func (c *Container) Pid() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.process.Pid()
}
