package containerd

import "time"

type EventType int

func (t EventType) String() string {
	switch t {
	case ExitEvent:
		return "exit"
	case PausedEvent:
		return "paused"
	case CreateEvent:
		return "create"
	case StartEvent:
		return "start"
	case OOMEvent:
		return "oom"
	case ExecAddEvent:
		return "execAdd"
	}
	return "unknown"
}

const (
	ExitEvent EventType = iota + 1
	PausedEvent
	CreateEvent
	StartEvent
	OOMEvent
	ExecAddEvent
)

type Event struct {
	Timestamp  time.Time
	Type       EventType
	Runtime    string
	ID         string
	Pid        uint32
	ExitStatus uint32
}

type EventWriter interface {
	Write(*Event) error
}

type EventFilter func(*Event) bool

// NewFilterEventWriter returns an EventWriter that runs the provided filters on the events.
// If all the filters pass then the event is written to the wrapped EventWriter
func NewFilterEventWriter(w EventWriter, filters ...EventFilter) EventWriter {
	return &filteredEventWriter{
		w:       w,
		filters: filters,
	}
}

type filteredEventWriter struct {
	w       EventWriter
	filters []EventFilter
}

func (f *filteredEventWriter) Write(e *Event) error {
	for _, filter := range f.filters {
		if !filter(e) {
			return nil
		}
	}
	return f.w.Write(e)
}
