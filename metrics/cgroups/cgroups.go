package cgroups

import (
	"time"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/plugin"
	"github.com/crosbymichael/cgroups"
	"github.com/crosbymichael/cgroups/prometheus"
	metrics "github.com/docker/go-metrics"
	"golang.org/x/net/context"
)

const name = "cgroups"

func init() {
	plugin.Register(name, &plugin.Registration{
		Type: plugin.ContainerMonitorPlugin,
		Init: New,
	})
}

func New(ic *plugin.InitContext) (interface{}, error) {
	var (
		ns        = metrics.NewNamespace("container", "", nil)
		collector = prometheus.New(ns)
	)
	oom, err := prometheus.NewOOMCollector(ns)
	if err != nil {
		return nil, err
	}
	metrics.Register(ns)
	return &cgroupsMonitor{
		collector: collector,
		oom:       oom,
		context:   ic.Context,
	}, nil
}

type cgroupsMonitor struct {
	collector *prometheus.Collector
	oom       *prometheus.OOMCollector
	context   context.Context
	events    chan<- *containerd.Event
}

func (m *cgroupsMonitor) Monitor(c containerd.Container) error {
	// skip non-linux containers
	if _, ok := c.(containerd.LinuxContainer); !ok {
		return nil
	}
	id := c.Info().ID
	state, err := c.State(m.context)
	if err != nil {
		return err
	}
	cg, err := cgroups.Load(cgroups.V1, cgroups.PidPath(int(state.Pid())))
	if err != nil {
		return err
	}
	if err := m.collector.Add(id, cg); err != nil {
		return err
	}
	return m.oom.Add(id, cg, m.trigger)
}

func (m *cgroupsMonitor) Stop(c containerd.Container) error {
	if _, ok := c.(containerd.LinuxContainer); !ok {
		return nil
	}
	m.collector.Remove(c.Info().ID)
	return nil
}

func (m *cgroupsMonitor) Events(events chan<- *containerd.Event) {
	m.events = events
}

func (m *cgroupsMonitor) trigger(id string, cg cgroups.Cgroup) {
	m.events <- &containerd.Event{
		Timestamp: time.Now(),
		Type:      containerd.OOMEvent,
		ID:        id,
	}
}
