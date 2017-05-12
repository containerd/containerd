package plugin

import "github.com/containerd/containerd"

// ContainerMonitor provides an interface for monitoring of containers within containerd
type ContainerMonitor interface {
	// Monitor adds the provided container to the monitor
	Monitor(Container) error
	// Stop stops and removes the provided container from the monitor
	Stop(Container) error
	// Events emits events from the monitor
	Events(chan<- *containerd.Event)
}

func NewMultiContainerMonitor(monitors ...ContainerMonitor) ContainerMonitor {
	return &multiContainerMonitor{
		monitors: monitors,
	}
}

func NewNoopMonitor() ContainerMonitor {
	return &noopContainerMonitor{}
}

type noopContainerMonitor struct {
}

func (mm *noopContainerMonitor) Monitor(c Container) error {
	return nil
}

func (mm *noopContainerMonitor) Stop(c Container) error {
	return nil
}

func (mm *noopContainerMonitor) Events(events chan<- *containerd.Event) {
}

type multiContainerMonitor struct {
	monitors []ContainerMonitor
}

func (mm *multiContainerMonitor) Monitor(c Container) error {
	for _, m := range mm.monitors {
		if err := m.Monitor(c); err != nil {
			return err
		}
	}
	return nil
}

func (mm *multiContainerMonitor) Stop(c Container) error {
	for _, m := range mm.monitors {
		if err := m.Stop(c); err != nil {
			return err
		}
	}
	return nil
}

func (mm *multiContainerMonitor) Events(events chan<- *containerd.Event) {
	for _, m := range mm.monitors {
		m.Events(events)
	}
}
