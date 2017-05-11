package prometheus

import (
	"sync"

	"golang.org/x/sys/unix"

	"github.com/Sirupsen/logrus"
	"github.com/containerd/cgroups"
	metrics "github.com/docker/go-metrics"
)

func NewOOMCollector(ns *metrics.Namespace) (*OOMCollector, error) {
	fd, err := unix.EpollCreate1(unix.EPOLL_CLOEXEC)
	if err != nil {
		return nil, err
	}
	c := &OOMCollector{
		fd:        fd,
		memoryOOM: ns.NewLabeledGauge("memory_oom", "The number of times a container received an oom event", metrics.Total, "id"),
		set:       make(map[uintptr]*oom),
	}
	go c.start()
	return c, nil
}

type OOMCollector struct {
	mu sync.Mutex

	memoryOOM metrics.LabeledGauge
	fd        int
	set       map[uintptr]*oom
}

type oom struct {
	id       string
	c        cgroups.Cgroup
	triggers []Trigger
}

func (o *OOMCollector) Add(id string, cg cgroups.Cgroup, triggers ...Trigger) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	fd, err := cg.OOMEventFD()
	if err != nil {
		return err
	}
	o.set[fd] = &oom{
		id:       id,
		c:        cg,
		triggers: triggers,
	}
	// set the gauge's default value
	o.memoryOOM.WithValues(id).Set(0)
	event := unix.EpollEvent{
		Fd:     int32(fd),
		Events: unix.EPOLLHUP | unix.EPOLLIN | unix.EPOLLERR,
	}
	if err := unix.EpollCtl(o.fd, unix.EPOLL_CTL_ADD, int(fd), &event); err != nil {
		return err
	}
	return nil
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
	o.memoryOOM.WithValues(info.id).Inc(1)
	for _, t := range info.triggers {
		t(info.id, info.c)
	}
}

func flush(fd uintptr) error {
	buf := make([]byte, 8)
	_, err := unix.Read(int(fd), buf)
	return err
}
