// +build linux

package cgroups

import (
	"time"

	"github.com/containerd/cgroups"
	events "github.com/containerd/containerd/api/services/events/v1"
	"github.com/containerd/containerd/plugin"
	"github.com/containerd/containerd/runtime"
	metrics "github.com/docker/go-metrics"
	"golang.org/x/net/context"
)

func init() {
	plugin.Register(&plugin.Registration{
		Type: plugin.TaskMonitorPlugin,
		ID:   "cgroups",
		Init: New,
	})
}

func New(ic *plugin.InitContext) (interface{}, error) {
	var (
		ns        = metrics.NewNamespace("container", "", nil)
		collector = NewCollector(ns)
	)
	oom, err := NewOOMCollector(ns)
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
	collector *Collector
	oom       *OOMCollector
	context   context.Context
	events    chan<- *events.RuntimeEvent
}

func (m *cgroupsMonitor) Monitor(c runtime.Task) error {
	info := c.Info()
	state, err := c.State(m.context)
	if err != nil {
		return err
	}
	cg, err := cgroups.Load(cgroups.V1, cgroups.PidPath(int(state.Pid)))
	if err != nil {
		return err
	}
	if err := m.collector.Add(info.ID, info.Namespace, cg); err != nil {
		return err
	}
	return m.oom.Add(info.ID, info.Namespace, cg, m.trigger)
}

func (m *cgroupsMonitor) Stop(c runtime.Task) error {
	info := c.Info()
	m.collector.Remove(info.ID, info.Namespace)
	return nil
}

func (m *cgroupsMonitor) Events(events chan<- *events.RuntimeEvent) {
	m.events = events
}

func (m *cgroupsMonitor) trigger(id string, cg cgroups.Cgroup) {
	m.events <- &events.RuntimeEvent{
		Timestamp: time.Now(),
		Type:      events.RuntimeEvent_OOM,
		ID:        id,
	}
}
