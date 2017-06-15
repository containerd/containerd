// +build linux

package cgroups

import (
	"fmt"
	"time"

	"github.com/containerd/cgroups"
	"github.com/containerd/cgroups/prometheus"
	"github.com/containerd/containerd/plugin"
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
	events    chan<- *plugin.Event
}

func getID(t plugin.Task) string {
	return fmt.Sprintf("%s-%s", t.Info().Namespace, t.Info().ID)
}

func (m *cgroupsMonitor) Monitor(c plugin.Task) error {
	id := getID(c)
	state, err := c.State(m.context)
	if err != nil {
		return err
	}
	cg, err := cgroups.Load(cgroups.V1, cgroups.PidPath(int(state.Pid)))
	if err != nil {
		return err
	}
	if err := m.collector.Add(id, cg); err != nil {
		return err
	}
	return m.oom.Add(id, cg, m.trigger)
}

func (m *cgroupsMonitor) Stop(c plugin.Task) error {
	m.collector.Remove(getID(c))
	return nil
}

func (m *cgroupsMonitor) Events(events chan<- *plugin.Event) {
	m.events = events
}

func (m *cgroupsMonitor) trigger(id string, cg cgroups.Cgroup) {
	m.events <- &plugin.Event{
		Timestamp: time.Now(),
		Type:      plugin.OOMEvent,
		ID:        id,
	}
}
