// +build linux

package cgroups

import (
	"github.com/containerd/cgroups"
	eventsapi "github.com/containerd/containerd/api/services/events/v1"
	"github.com/containerd/containerd/events"
	"github.com/containerd/containerd/linux"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/namespaces"
	"github.com/containerd/containerd/plugin"
	"github.com/containerd/containerd/runtime"
	metrics "github.com/docker/go-metrics"
	"golang.org/x/net/context"
)

type Config struct {
	NoPrometheus bool `toml:"no_prometheus"`
}

func init() {
	plugin.Register(&plugin.Registration{
		Type:   plugin.TaskMonitorPlugin,
		ID:     "cgroups",
		Init:   New,
		Config: &Config{},
	})
}

func New(ic *plugin.InitContext) (interface{}, error) {
	var ns *metrics.Namespace
	config := ic.Config.(*Config)
	if !config.NoPrometheus {
		ns = metrics.NewNamespace("container", "", nil)
	}
	collector := NewCollector(ns)
	oom, err := NewOOMCollector(ns)
	if err != nil {
		return nil, err
	}
	if ns != nil {
		metrics.Register(ns)
	}
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
	t := c.(*linux.Task)
	if err := m.collector.Add(info.ID, info.Namespace, t.Cgroup()); err != nil {
		return err
	}
	return m.oom.Add(info.ID, info.Namespace, t.Cgroup(), m.trigger)
}

func (m *cgroupsMonitor) Stop(c runtime.Task) error {
	info := c.Info()
	m.collector.Remove(info.ID, info.Namespace)
	return nil
}

func (m *cgroupsMonitor) trigger(id, namespace string, cg cgroups.Cgroup) {
	ctx := namespaces.WithNamespace(m.context, namespace)
	if err := m.publisher.Publish(ctx, runtime.TaskOOMEventTopic, &eventsapi.TaskOOM{
		ContainerID: id,
	}); err != nil {
		log.G(m.context).WithError(err).Error("post OOM event")
	}
}
