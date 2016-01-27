package supervisor

import (
	"os"
	"time"

	"github.com/docker/containerd/runtime"
	"github.com/opencontainers/specs"
)

type TaskType string

const (
	ExecExitTask         TaskType = "execExit"
	ExitTask             TaskType = "exit"
	StartContainerTask   TaskType = "startContainer"
	DeleteTask           TaskType = "deleteContainerEvent"
	GetContainerTask     TaskType = "getContainer"
	SignalTask           TaskType = "signal"
	AddProcessTask       TaskType = "addProcess"
	UpdateContainerTask  TaskType = "updateContainer"
	CreateCheckpointTask TaskType = "createCheckpoint"
	DeleteCheckpointTask TaskType = "deleteCheckpoint"
	StatsTask            TaskType = "events"
	UnsubscribeStatsTask TaskType = "unsubscribeStats"
	StopStatsTask        TaskType = "stopStats"
	OOMTask              TaskType = "oom"
)

func NewTask(t TaskType) *Task {
	return &Task{
		Type:      t,
		Timestamp: time.Now(),
		Err:       make(chan error, 1),
	}
}

type StartResponse struct {
	Pid int
}

type Task struct {
	Type          TaskType
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
	Handle(*Task) error
}

// data structure for sending tasks to eventloop
type commonTaskEvent struct {
	data *Task
	sv   *Supervisor
}

func (e *commonTaskEvent) Handle() {
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
