// +build linux

package cgroups

import (
	"sync"

	"golang.org/x/sys/unix"

	"github.com/containerd/cgroups"
	metrics "github.com/docker/go-metrics"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"
)

func NewOOMCollector(ns *metrics.Namespace) (*OOMCollector, error) {
	fd, err := unix.EpollCreate1(unix.EPOLL_CLOEXEC)
	if err != nil {
		return nil, err
	}
	c := &OOMCollector{
		fd:   fd,
		set:  make(map[uintptr]*oom),
		desc: ns.NewDesc("memory_oom", "The number of times a container has received an oom event", metrics.Total, "container_id", "namespace"),
	}
	go c.start()
	ns.Add(c)
	return c, nil
}

type OOMCollector struct {
	mu sync.Mutex

	fd   int
	set  map[uintptr]*oom
	desc *prometheus.Desc
}

type oom struct {
	id        string
	namespace string
	c         cgroups.Cgroup
	triggers  []Trigger
	count     int
}

func (o *OOMCollector) Add(id, namespace string, cg cgroups.Cgroup, triggers ...Trigger) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	fd, err := cg.OOMEventFD()
	if err != nil {
		return err
	}
	o.set[fd] = &oom{
		id:        id,
		namespace: namespace,
		c:         cg,
		triggers:  triggers,
	}
	event := unix.EpollEvent{
		Fd:     int32(fd),
		Events: unix.EPOLLHUP | unix.EPOLLIN | unix.EPOLLERR,
	}
	if err := unix.EpollCtl(o.fd, unix.EPOLL_CTL_ADD, int(fd), &event); err != nil {
		return err
	}
	return nil
}

func (o *OOMCollector) Remove(id, namespace string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	for fd, t := range o.set {
		if t.id == id && t.namespace == namespace {
			unix.Close(int(fd))
			delete(o.set, fd)
			return
		}
	}
}

// Close closes the epoll fd
func (o *OOMCollector) Close() error {
	return unix.Close(int(o.fd))
}

func (o *OOMCollector) Describe(ch chan<- *prometheus.Desc) {
	o.mu.Lock()
	defer o.mu.Unlock()
	ch <- o.desc
}

func (o *OOMCollector) Collect(ch chan<- prometheus.Metric) {
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, t := range o.set {
		ch <- prometheus.MustNewConstMetric(o.desc, prometheus.GaugeValue, float64(t.count), t.id, t.namespace)
	}
}

func (o *OOMCollector) start() {
	var events [128]unix.EpollEvent
	for {
		n, err := unix.EpollWait(o.fd, events[:], -1)
		if err != nil {
			if err == unix.EINTR {
				continue
			}
			logrus.WithField("error", err).Fatal("cgroups: epoll wait")
		}
		for i := 0; i < n; i++ {
			o.process(uintptr(events[i].Fd), events[i].Events)
		}
	}
}

func (o *OOMCollector) process(fd uintptr, event uint32) {
	// make sure to always flush the fd
	flush(fd)

	o.mu.Lock()
	info, ok := o.set[fd]
	if !ok {
		o.mu.Unlock()
		return
	}
	o.mu.Unlock()
	// if we received an event but it was caused by the cgroup being deleted and the fd
	// being closed make sure we close our copy and remove the container from the set
	if info.c.State() == cgroups.Deleted {
		o.mu.Lock()
		delete(o.set, fd)
		o.mu.Unlock()
		unix.Close(int(fd))
		return
	}
	info.count++
	for _, t := range info.triggers {
		t(info.id, info.c)
	}
}

func flush(fd uintptr) error {
	buf := make([]byte, 8)
	_, err := unix.Read(int(fd), buf)
	return err
}
