package supervisor

import "github.com/rcrowley/go-metrics"

var (
	ContainerCreateTimer   = metrics.NewTimer()
	ContainerDeleteTimer   = metrics.NewTimer()
	ContainerStartTimer    = metrics.NewTimer()
	ContainersCounter      = metrics.NewCounter()
	EventSubscriberCounter = metrics.NewCounter()
	TaskCounter            = metrics.NewCounter()
	ExecProcessTimer       = metrics.NewTimer()
	ExitProcessTimer       = metrics.NewTimer()
)

func Metrics() map[string]interface{} {
	return map[string]interface{}{
		"container-create-time": ContainerCreateTimer,
		"container-delete-time": ContainerDeleteTimer,
		"container-start-time":  ContainerStartTimer,
		"containers":            ContainersCounter,
		"event-subscribers":     EventSubscriberCounter,
		"events":                TaskCounter,
		"exec-process-time":     ExecProcessTimer,
		"exit-process-time":     ExitProcessTimer,
	}
}
