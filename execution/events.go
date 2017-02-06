package execution

import "time"

const (
	ExitEvent   = "exit"
	OOMEvent    = "oom"
	CreateEvent = "create"
	StartEvent  = "start"
	ExecEvent   = "exec-added9"
)

type ContainerEvent struct {
	Timestamp  time.Time
	ID         string
	Type       string
	Pid        uint32
	ExitStatus uint32
}

const (
	ContainersEventsSubjectSubscriber = "containerd.execution.container.>"
)

const (
	containerEventsTopicFormat        = "container.%s"
	containerProcessEventsTopicFormat = "container.%s.%s"
)
