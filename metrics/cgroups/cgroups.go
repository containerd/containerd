// +build linux

package cgroups

import (
	"github.com/containerd/cgroups"
	eventsapi "github.com/containerd/containerd/api/services/events/v1"
	"github.com/containerd/containerd/events"
	"github.com/containerd/containerd/log"
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
		publisher: ic.Events,
	}, nil
}

type cgroupsMonitor struct {
	collector *Collector
	oom       *OOMCollector
	context   context.Context
	publisher events.Publisher
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

func (m *cgroupsMonitor) trigger(id string, cg cgroups.Cgroup) {
	if err := m.publisher.Publish(m.context, runtime.TaskOOMEventTopic, &eventsapi.TaskOOM{
		ContainerID: id,
	}); err != nil {
		log.G(m.context).WithError(err).Error("post OOM event")
	}
}
