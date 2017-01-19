package execution

import (
	"context"
	"io/ioutil"
	"os"
	"path/filepath"
	"sync"

	"github.com/docker/containerd/log"
	"github.com/pkg/errors"
)

const (
	InitProcessID    = "init"
	processesDirName = "processes"
	bundleFileName   = "bundle"
)

func LoadContainer(ctx context.Context, stateDir, id string) (c *Container, err error) {
	c = &Container{
		id:        id,
		stateDir:  stateDir,
		processes: make(map[string]Process, 1),
		ctx:       ctx,
		status:    Unknown,
	}

	data, err := ioutil.ReadFile(filepath.Join(stateDir, bundleFileName))
	if err != nil {
		err = errors.Wrapf(err, "failed to read bundle path")
		return
	}
	c.bundle = string(data)

	return
}

func NewContainer(ctx context.Context, stateDir, id, bundle string) (c *Container, err error) {
	c = &Container{
		id:        id,
		stateDir:  stateDir,
		bundle:    bundle,
		processes: make(map[string]Process, 1),
		status:    Created,
		ctx:       ctx,
	}
	defer func() {
		if err != nil {
			c.Cleanup()
			c = nil
		}
	}()

	if err = os.Mkdir(stateDir, 0700); err != nil {
		err = errors.Wrap(err, "failed to create container state dir")
		return
	}

	bundleFile := filepath.Join(stateDir, bundleFileName)
	if err = ioutil.WriteFile(bundleFile, []byte(bundle), 0600); err != nil {
		err = errors.Wrap(err, "failed to store bundle path")
		return
	}

	processesDir := filepath.Join(stateDir, processesDirName)
	if err = os.Mkdir(processesDir, 0700); err != nil {
		err = errors.Wrap(err, "failed to create processes statedir")
		return
	}

	return
}

type Container struct {
	id        string
	stateDir  string
	bundle    string
	processes map[string]Process
	status    Status

	ctx context.Context
	mu  sync.Mutex
}

func (c *Container) ID() string {
	return c.id
}

func (c *Container) Bundle() string {
	return c.bundle
}

func (c *Container) Wait() (uint32, error) {
	initProcess := c.GetProcess(InitProcessID)
	return initProcess.Wait()
}

func (c *Container) Status() Status {
	initProcess := c.GetProcess(InitProcessID)
	return initProcess.Status()
}

func (c *Container) AddProcess(p Process) {
	c.mu.Lock()
	c.processes[p.ID()] = p
	c.mu.Unlock()
}

func (c *Container) RemoveProcess(id string) error {
	if _, ok := c.processes[id]; !ok {
		return errors.Errorf("no such process %s", id)
	}

	c.mu.Lock()
	delete(c.processes, id)
	c.mu.Unlock()

	processStateDir := filepath.Join(c.stateDir, processesDirName, id)
	err := os.RemoveAll(processStateDir)
	if err != nil {
		return errors.Wrap(err, "failed to remove process state dir")
	}

	return nil
}

func (c *Container) GetProcess(id string) Process {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.processes[id]
}

func (c *Container) Processes() []Process {
	var procs []Process

	c.mu.Lock()
	for _, p := range c.processes {
		procs = append(procs, p)
	}
	c.mu.Unlock()

	return procs
}

// ProcessStateDir returns the path of the state dir for a given
// process id. The process doesn't have to exist for this to succeed.
func (c *Container) ProcessStateDir(id string) string {
	return filepath.Join(c.stateDir, processesDirName, id)
}

// ProcessesStateDir returns a map matching process ids to their state
// directory
func (c *Container) ProcessesStateDir() (map[string]string, error) {
	root := filepath.Join(c.stateDir, processesDirName)
	dirs, err := ioutil.ReadDir(root)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to list processes state dirs")
	}

	procs := make(map[string]string, 1)
	for _, d := range dirs {
		if d.IsDir() {
			procs[d.Name()] = filepath.Join(root, d.Name())
		}
	}

	return procs, nil
}

func (c *Container) Cleanup() {
	if err := os.RemoveAll(c.stateDir); err != nil {
		log.G(c.ctx).Warnf("failed to remove container state dir: %v", err)
	}
}

func (c *Container) Context() context.Context {
	return c.ctx
}
