package process

import (
	"context"
	"io"
	"os"
	"sync"
	"syscall"
	"time"

	"github.com/containerd/console"
	shim_process "github.com/containerd/containerd/v2/cmd/containerd-shim-runc-v2/process"
	"github.com/containerd/containerd/v2/pkg/stdio"
)

var _ shim_process.Process = &Init{}

// Init represents an initial process for a container
type Init struct {
	wg        sync.WaitGroup
	initState initState

	// mu is used to ensure that `Start()` and `Exited()` calls return in
	// the right order when invoked in separate goroutines.
	// This is the case within the shim implementation as it makes use of
	// the reaper interface.
	mu sync.Mutex

	waitBlock chan struct{}

	WorkDir string

	id     string
	Bundle string
	status int
	exited time.Time
	pid    int
	stdin  io.Closer
	stdio  stdio.Stdio
	Rootfs string
}

// New returns a new process
func New(id string, stdio stdio.Stdio) *Init {
	p := &Init{
		id:        id,
		stdio:     stdio,
		status:    0,
		waitBlock: make(chan struct{}),
	}
	p.initState = &createdState{p: p, status: "created"}
	return p
}

// Create the process with the provided config
func (p *Init) Create(ctx context.Context, r *shim_process.CreateConfig) error {
	return nil
}

// Wait for the process to exit
func (p *Init) Wait() {
	<-p.waitBlock
}

// ID of the process
func (p *Init) ID() string {
	return p.id
}

// Pid of the process
func (p *Init) Pid() int {
	return p.pid
}

// ExitStatus of the process
func (p *Init) ExitStatus() int {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.status
}

// ExitedAt at time when the process exited
func (p *Init) ExitedAt() time.Time {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.exited
}

// Status of the process
func (p *Init) Status(ctx context.Context) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.initState.Status(ctx)
}

// Start the init process
func (p *Init) Start(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.initState.Start(ctx)
}

// SetExited of the init process with the next status
func (p *Init) SetExited(status int) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.initState.SetExited(status)
}

func (p *Init) setExited(status int) {
	p.exited = time.Now()
	p.status = status
	close(p.waitBlock)
}

// Kill the init process
func (p *Init) Kill(ctx context.Context, signal uint32, all bool) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.initState.Kill(ctx, signal, all)
}

func (p *Init) kill(ctx context.Context, signal uint32, all bool) error {
	if p.pid != 0 {
		process, err := os.FindProcess(p.pid)
		if err != nil {
			return err
		}
		if err := process.Signal(syscall.Signal(signal)); err != nil {
			return err
		}
		processState, err := process.Wait()
		if err != nil {
			return err
		}
		p.setExited(processState.ExitCode())

		p.pid = 0
	} else {
		p.setExited(0)
	}

	return nil
}

// Stdin of the process
func (p *Init) Stdin() io.Closer {
	return p.stdin
}

// Stdio of the process
func (p *Init) Stdio() stdio.Stdio {
	return p.stdio
}

// Delete the init process
func (p *Init) Delete(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.initState.Delete(ctx)
}

// Resize the init processes console
func (p *Init) Resize(ws console.WinSize) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	return nil
}
