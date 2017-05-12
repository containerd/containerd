package plugin

import "github.com/containerd/containerd"

// ContainerMonitor provides an interface for monitoring of containers within containerd
type ContainerMonitor interface {
	// Monitor adds the provided container to the monitor
	Monitor(containerd.Container) error
	// Stop stops and removes the provided container from the monitor
	Stop(containerd.Container) error
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

func (mm *noopContainerMonitor) Monitor(c containerd.Container) error {
	return nil
}

func (mm *noopContainerMonitor) Stop(c containerd.Container) error {
	return nil
}

type multiContainerMonitor struct {
	monitors []ContainerMonitor
}

func (mm *multiContainerMonitor) Monitor(c containerd.Container) error {
	for _, m := range mm.monitors {
		if err := m.Monitor(c); err != nil {
			return err
		}
	}
	return nil
}

func (mm *multiContainerMonitor) Stop(c containerd.Container) error {
	for _, m := range mm.monitors {
		if err := m.Stop(c); err != nil {
			return err
		}
	}
	return nil
}
