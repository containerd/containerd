// +build linux

package cgroups

import (
	"errors"
	"fmt"
	"sync"

	"github.com/containerd/cgroups"
	metrics "github.com/docker/go-metrics"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"
)

var (
	ErrAlreadyCollected = errors.New("cgroup is already being collected")
	ErrCgroupNotExists  = errors.New("cgroup does not exist in the collector")
)

// Trigger will be called when an event happens and provides the cgroup
// where the event originated from
type Trigger func(string, string, cgroups.Cgroup)

// New registers the Collector with the provided namespace and returns it so
// that cgroups can be added for collection
func NewCollector(ns *metrics.Namespace) *Collector {
	if ns == nil {
		return &Collector{}
	}
	// add machine cpus and memory info
	c := &Collector{
		ns:      ns,
		cgroups: make(map[string]*task),
	}
	c.metrics = append(c.metrics, pidMetrics...)
	c.metrics = append(c.metrics, cpuMetrics...)
	c.metrics = append(c.metrics, memoryMetrics...)
	c.metrics = append(c.metrics, hugetlbMetrics...)
	c.metrics = append(c.metrics, blkioMetrics...)
	ns.Add(c)
	return c
}

type task struct {
	id        string
	namespace string
	cgroup    cgroups.Cgroup
}

func taskID(id, namespace string) string {
	return fmt.Sprintf("%s-%s", id, namespace)
}

// Collector provides the ability to collect container stats and export
// them in the prometheus format
type Collector struct {
	mu sync.RWMutex

	cgroups map[string]*task
	ns      *metrics.Namespace
	metrics []*metric
}

func (c *Collector) Describe(ch chan<- *prometheus.Desc) {
	for _, m := range c.metrics {
		ch <- m.desc(c.ns)
	}
}

func (c *Collector) Collect(ch chan<- prometheus.Metric) {
	c.mu.RLock()
	wg := &sync.WaitGroup{}
	for _, t := range c.cgroups {
		wg.Add(1)
		go c.collect(t.id, t.namespace, t.cgroup, ch, wg)
	}
	c.mu.RUnlock()
	wg.Wait()
}

func (c *Collector) collect(id, namespace string, cg cgroups.Cgroup, ch chan<- prometheus.Metric, wg *sync.WaitGroup) {
	defer wg.Done()
	stats, err := cg.Stat(cgroups.IgnoreNotExist)
	if err != nil {
		logrus.WithError(err).Errorf("stat cgroup %s", id)
		return
	}
	for _, m := range c.metrics {
		m.collect(id, namespace, stats, c.ns, ch)
	}
}

// Add adds the provided cgroup and id so that metrics are collected and exported
func (c *Collector) Add(id, namespace string, cg cgroups.Cgroup) error {
	if c.ns == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.cgroups[taskID(id, namespace)]; ok {
		return ErrAlreadyCollected
	}
	c.cgroups[taskID(id, namespace)] = &task{
		id:        id,
		namespace: namespace,
		cgroup:    cg,
	}
	return nil
}

// Remove removes the provided cgroup by id from the collector
func (c *Collector) Remove(id, namespace string) {
	if c.ns == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.cgroups, taskID(id, namespace))
}
