// +build linux

/*
   Copyright The containerd Authors.

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.
*/

package runc

import (
	"context"
	"io/ioutil"
	"path/filepath"
	"sync"

	"github.com/containerd/cgroups"
	"github.com/containerd/console"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/mount"
	"github.com/containerd/containerd/namespaces"
	rproc "github.com/containerd/containerd/runtime/proc"
	"github.com/containerd/containerd/runtime/v1/linux/proc"
	"github.com/containerd/containerd/runtime/v2/runc/options"
	"github.com/containerd/containerd/runtime/v2/task"
	"github.com/containerd/typeurl"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

func NewContainer(ctx context.Context, platform rproc.Platform, r *task.CreateTaskRequest) (*Container, error) {
	ns, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "create namespace")
	}

	var opts options.Options
	if r.Options != nil {
		v, err := typeurl.UnmarshalAny(r.Options)
		if err != nil {
			return nil, err
		}
		opts = *v.(*options.Options)
	}

	var mounts []proc.Mount
	for _, m := range r.Rootfs {
		mounts = append(mounts, proc.Mount{
			Type:    m.Type,
			Source:  m.Source,
			Target:  m.Target,
			Options: m.Options,
		})
	}
	config := &proc.CreateConfig{
		ID:               r.ID,
		Bundle:           r.Bundle,
		Runtime:          opts.BinaryName,
		Rootfs:           mounts,
		Terminal:         r.Terminal,
		Stdin:            r.Stdin,
		Stdout:           r.Stdout,
		Stderr:           r.Stderr,
		Checkpoint:       r.Checkpoint,
		ParentCheckpoint: r.ParentCheckpoint,
		Options:          r.Options,
	}

	if err := WriteRuntime(r.Bundle, opts.BinaryName); err != nil {
		return nil, err
	}
	rootfs := filepath.Join(r.Bundle, "rootfs")
	defer func() {
		if err != nil {
			if err2 := mount.UnmountAll(rootfs, 0); err2 != nil {
				logrus.WithError(err2).Warn("failed to cleanup rootfs mount")
			}
		}
	}()
	for _, rm := range mounts {
		m := &mount.Mount{
			Type:    rm.Type,
			Source:  rm.Source,
			Options: rm.Options,
		}
		if err := m.Mount(rootfs); err != nil {
			return nil, errors.Wrapf(err, "failed to mount rootfs component %v", m)
		}
	}

	process, err := newInit(
		ctx,
		r.Bundle,
		filepath.Join(r.Bundle, "work"),
		ns,
		platform,
		config,
		&opts,
	)
	if err != nil {
		return nil, errdefs.ToGRPC(err)
	}
	if err := process.Create(ctx, config); err != nil {
		return nil, errdefs.ToGRPC(err)
	}
	container := &Container{
		ID:        r.ID,
		Bundle:    r.Bundle,
		process:   process,
		processes: make(map[string]rproc.Process),
	}
	pid := process.Pid()
	if pid > 0 {
		cg, err := cgroups.Load(cgroups.V1, cgroups.PidPath(pid))
		if err != nil {
			logrus.WithError(err).Errorf("loading cgroup for %d", pid)
		}
		container.cgroup = cg
	}
	return container, nil
}

func ReadRuntime(path string) (string, error) {
	data, err := ioutil.ReadFile(filepath.Join(path, "runtime"))
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func WriteRuntime(path, runtime string) error {
	return ioutil.WriteFile(filepath.Join(path, "runtime"), []byte(runtime), 0600)
}

func newInit(ctx context.Context, path, workDir, namespace string, platform rproc.Platform,
	r *proc.CreateConfig, options *options.Options) (*proc.Init, error) {
	rootfs := filepath.Join(path, "rootfs")
	runtime := proc.NewRunc(options.Root, path, namespace, options.BinaryName, options.CriuPath, options.SystemdCgroup)
	p := proc.New(r.ID, runtime, rproc.Stdio{
		Stdin:    r.Stdin,
		Stdout:   r.Stdout,
		Stderr:   r.Stderr,
		Terminal: r.Terminal,
	})
	p.Bundle = r.Bundle
	p.Platform = platform
	p.Rootfs = rootfs
	p.WorkDir = workDir
	p.IoUID = int(options.IoUid)
	p.IoGID = int(options.IoGid)
	p.NoPivotRoot = options.NoPivotRoot
	p.NoNewKeyring = options.NoNewKeyring
	p.CriuWorkPath = options.CriuWorkPath
	if p.CriuWorkPath == "" {
		// if criu work path not set, use container WorkDir
		p.CriuWorkPath = p.WorkDir
	}
	return p, nil
}

type Container struct {
	mu sync.Mutex

	ID     string
	Bundle string

	cgroup    cgroups.Cgroup
	process   rproc.Process
	processes map[string]rproc.Process
}

func (c *Container) All() (o []rproc.Process) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, p := range c.processes {
		o = append(o, p)
	}
	if c.process != nil {
		o = append(o, c.process)
	}
	return o
}

func (c *Container) ExecdProcesses() (o []rproc.Process) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, p := range c.processes {
		o = append(o, p)
	}
	return o
}

// Pid of the main process of a container
func (c *Container) Pid() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.process.Pid()
}

func (c *Container) Cgroup() cgroups.Cgroup {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cgroup
}

func (c *Container) CgroupSet(cg cgroups.Cgroup) {
	c.mu.Lock()
	c.cgroup = cg
	c.mu.Unlock()
}

func (c *Container) Process(id string) rproc.Process {
	c.mu.Lock()
	defer c.mu.Unlock()
	if id == "" {
		return c.process
	}
	return c.processes[id]
}

func (c *Container) ProcessExists(id string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.processes[id]
	return ok
}

func (c *Container) ProcessAdd(process rproc.Process) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.processes[process.ID()] = process
}

func (c *Container) ProcessRemove(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.processes, id)
}

func (c *Container) Start(ctx context.Context, r *task.StartRequest) (rproc.Process, error) {
	p := c.Process(r.ExecID)
	if p == nil {
		return nil, errors.Wrapf(errdefs.ErrNotFound, "process does not exist %s", r.ExecID)
	}
	if err := p.Start(ctx); err != nil {
		return nil, err
	}
	if c.Cgroup() == nil && p.Pid() > 0 {
		cg, err := cgroups.Load(cgroups.V1, cgroups.PidPath(p.Pid()))
		if err != nil {
			logrus.WithError(err).Errorf("loading cgroup for %d", p.Pid())
		}
		c.cgroup = cg
	}
	return p, nil
}

func (c *Container) Delete(ctx context.Context, r *task.DeleteRequest) (rproc.Process, error) {
	p := c.Process(r.ExecID)
	if p == nil {
		return nil, errors.Wrapf(errdefs.ErrNotFound, "process does not exist %s", r.ExecID)
	}
	if err := p.Delete(ctx); err != nil {
		return nil, err
	}
	if r.ExecID != "" {
		c.ProcessRemove(r.ExecID)
	}
	return p, nil
}

func (c *Container) Exec(ctx context.Context, r *task.ExecProcessRequest) (rproc.Process, error) {
	process, err := c.process.(*proc.Init).Exec(ctx, c.Bundle, &proc.ExecConfig{
		ID:       r.ExecID,
		Terminal: r.Terminal,
		Stdin:    r.Stdin,
		Stdout:   r.Stdout,
		Stderr:   r.Stderr,
		Spec:     r.Spec,
	})
	if err != nil {
		return nil, err
	}
	c.ProcessAdd(process)
	return process, nil
}

func (c *Container) Pause(ctx context.Context) error {
	return c.process.(*proc.Init).Pause(ctx)
}

func (c *Container) Resume(ctx context.Context) error {
	return c.process.(*proc.Init).Resume(ctx)
}

func (c *Container) ResizePty(ctx context.Context, r *task.ResizePtyRequest) error {
	p := c.Process(r.ExecID)
	if p == nil {
		return errors.Wrapf(errdefs.ErrNotFound, "process does not exist %s", r.ExecID)
	}
	ws := console.WinSize{
		Width:  uint16(r.Width),
		Height: uint16(r.Height),
	}
	return p.Resize(ws)
}

func (c *Container) Kill(ctx context.Context, r *task.KillRequest) error {
	p := c.Process(r.ExecID)
	if p == nil {
		return errors.Wrapf(errdefs.ErrNotFound, "process does not exist %s", r.ExecID)
	}
	return p.Kill(ctx, r.Signal, r.All)
}

func (c *Container) CloseIO(ctx context.Context, r *task.CloseIORequest) error {
	p := c.Process(r.ExecID)
	if p == nil {
		return errors.Wrapf(errdefs.ErrNotFound, "process does not exist %s", r.ExecID)
	}
	if stdin := p.Stdin(); stdin != nil {
		if err := stdin.Close(); err != nil {
			return errors.Wrap(err, "close stdin")
		}
	}
	return nil
}

func (c *Container) Checkpoint(ctx context.Context, r *task.CheckpointTaskRequest) error {
	p := c.Process("")
	if p == nil {
		return errdefs.ToGRPCf(errdefs.ErrFailedPrecondition, "container must be created")
	}
	var opts options.CheckpointOptions
	if r.Options != nil {
		v, err := typeurl.UnmarshalAny(r.Options)
		if err != nil {
			return err
		}
		opts = *v.(*options.CheckpointOptions)
	}
	return p.(*proc.Init).Checkpoint(ctx, &proc.CheckpointConfig{
		Path:                     r.Path,
		Exit:                     opts.Exit,
		AllowOpenTCP:             opts.OpenTcp,
		AllowExternalUnixSockets: opts.ExternalUnixSockets,
		AllowTerminal:            opts.Terminal,
		FileLocks:                opts.FileLocks,
		EmptyNamespaces:          opts.EmptyNamespaces,
		WorkDir:                  opts.WorkPath,
	})
}

func (c *Container) Update(ctx context.Context, r *task.UpdateTaskRequest) error {
	p := c.Process("")
	if p == nil {
		return errdefs.ToGRPCf(errdefs.ErrFailedPrecondition, "container must be created")
	}
	return p.(*proc.Init).Update(ctx, r.Resources)
}
