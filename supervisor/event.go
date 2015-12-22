package supervisor

import (
	"os"
	"time"

	"github.com/docker/containerd/runtime"
	"github.com/opencontainers/specs"
)

type EventType string

const (
	ExecExitEventType         EventType = "execExit"
	ExitEventType             EventType = "exit"
	StartContainerEventType   EventType = "startContainer"
	DeleteEventType           EventType = "deleteContainerEvent"
	GetContainerEventType     EventType = "getContainer"
	SignalEventType           EventType = "signal"
	AddProcessEventType       EventType = "addProcess"
	UpdateContainerEventType  EventType = "updateContainer"
	CreateCheckpointEventType EventType = "createCheckpoint"
	DeleteCheckpointEventType EventType = "deleteCheckpoint"
	StatsEventType            EventType = "events"
	UnsubscribeStatsEventType EventType = "unsubscribeStats"
	StopStatsEventType        EventType = "stopStats"
	OOMEventType              EventType = "oom"
)

func NewEvent(t EventType) *Event {
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
	Type          EventType
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

// rpcEvent returns event for rpc listeners
func (e *Event) rpcEvent() *Event {
	return &Event{
		Type:   e.Type,
		ID:     e.ID,
		Status: e.Status,
		Pid:    e.Pid,
	}
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
	e.sv.notifySubscribers(e.data.rpcEvent())
	if err != errDeferedResponse {
		e.data.Err <- err
		close(e.data.Err)
		return
	}
}
