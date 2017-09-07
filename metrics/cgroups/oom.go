// +build linux

package cgroups

import (
	"sync"
	"sync/atomic"

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
	var desc *prometheus.Desc
	if ns != nil {
		desc = ns.NewDesc("memory_oom", "The number of times a container has received an oom event", metrics.Total, "container_id", "namespace")
	}
	c := &OOMCollector{
		fd:   fd,
		desc: desc,
		set:  make(map[uintptr]*oom),
	}
	if ns != nil {
		ns.Add(c)
	}
	go c.start()
	return c, nil
}

type OOMCollector struct {
	mu sync.Mutex

	desc *prometheus.Desc
	fd   int
	set  map[uintptr]*oom
}

type oom struct {
	id        string
	namespace string
	c         cgroups.Cgroup
	triggers  []Trigger
	count     int64
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
		c:         cg,
		triggers:  triggers,
		namespace: namespace,
	}
	event := unix.EpollEvent{
		Fd:     int32(fd),
		Events: unix.EPOLLHUP | unix.EPOLLIN | unix.EPOLLERR,
	}
	return unix.EpollCtl(o.fd, unix.EPOLL_CTL_ADD, int(fd), &event)
}

func (o *OOMCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- o.desc
}

func (o *OOMCollector) Collect(ch chan<- prometheus.Metric) {
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, t := range o.set {
		c := atomic.LoadInt64(&t.count)
		ch <- prometheus.MustNewConstMetric(o.desc, prometheus.CounterValue, float64(c), t.id, t.namespace)
	}
}

// Close closes the epoll fd
func (o *OOMCollector) Close() error {
	return unix.Close(int(o.fd))
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
	atomic.AddInt64(&info.count, 1)
	for _, t := range info.triggers {
		t(info.id, info.namespace, info.c)
	}
}

func flush(fd uintptr) error {
	var buf [8]byte
	_, err := unix.Read(int(fd), buf[:])
	return err
}
