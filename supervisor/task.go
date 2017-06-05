package supervisor

import (
	"context"
	"sync"

	"github.com/containerd/containerd/runtime"
)

// StartResponse is the response containing a started container
type StartResponse struct {
	ExecPid   int
	Container runtime.Container
}

// Task executes an action returning an error chan with either nil or
// the error from executing the task
type Task interface {
	// ErrorCh returns a channel used to report and error from an async task
	ErrorCh() chan error
	// Ctx carries the context of a task
	Ctx() context.Context
}

type baseTask struct {
	errCh chan error
	ctx   context.Context
	mu    sync.Mutex
}

func (t *baseTask) WithContext(ctx context.Context) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.ctx = ctx
}

func (t *baseTask) Ctx() context.Context {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.ctx
}

func (t *baseTask) ErrorCh() chan error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.errCh == nil {
		t.errCh = make(chan error, 1)
	}
	return t.errCh
}
