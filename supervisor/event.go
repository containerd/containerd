package supervisor

import (
	"os"
	"time"

	"github.com/docker/containerd/api/grpc/types"
	"github.com/docker/containerd/runtime"
	"github.com/opencontainers/specs"
)

func NewEvent(t types.EventType) *Event {
	return &Event{
		Type:      t,
		Timestamp: time.Now(),
		Err:       make(chan error, 1),
	}
}

type StartResponse struct {
	Pid int
}

type Event struct {
	Type          types.EventType
	Timestamp     time.Time
	ID            string
	BundlePath    string
	Stdout        string
	Stderr        string
	Stdin         string
	Console       string
	Pid           int
	Status        int
	Signal        os.Signal
	Process       *specs.Process
	State         *runtime.State
	Containers    []runtime.Container
	Checkpoint    *runtime.Checkpoint
	Err           chan error
	StartResponse chan StartResponse
	Stats         chan interface{}
}

type Handler interface {
	Handle(*Event) error
}

type commonEvent struct {
	data *Event
	sv   *Supervisor
}

func (e *commonEvent) Handle() {
	h, ok := e.sv.handlers[e.data.Type]
	if !ok {
		e.data.Err <- ErrUnknownEvent
		return
	}
	err := h.Handle(e.data)
	if err != errDeferedResponse {
		e.data.Err <- err
		close(e.data.Err)
		return
	}
}
