// +build solaris,cgo

package supervisor

/*
#include <port.h>
#include <poll.h>
#include <stdio.h>
#include <stdlib.h>
#include <unistd.h>

int portAssociate(int port, int fd) {
	if (port_associate(port, PORT_SOURCE_FD, fd, POLLIN | POLLHUP, NULL) < 0) {
		return 1;
	}
}

port_event_t* getEvent(int e_fd) {
	port_event_t *ev;
	ev = (port_event_t *)malloc(sizeof(port_event_t));
	if (port_get(e_fd, ev, NULL) < 0) {
		return NULL;
	}
	return ev;
}

int getFd(uintptr_t x) {
	return *(int *)x;
}

void freeEvent( port_event_t *ev){
	free(ev);
}
*/
import "C"
import (
	"sync"
	"unsafe"

	"github.com/Sirupsen/logrus"
	"github.com/docker/containerd/runtime"
)

// NewMonitor starts a new process monitor and returns it
func NewMonitor() (*Monitor, error) {
	m := &Monitor{
		receivers: make(map[int]interface{}),
		exits:     make(chan runtime.Process, 1024),
		ooms:      make(chan string, 1024),
	}
	fd, err := C.port_create()
	if err != nil {
		return nil, err
	}

	m.epollFd = int(fd)
	go m.start()
	return m, nil
}

// Monitor represents a runtime.Process monitor
type Monitor struct {
	m         sync.Mutex
	receivers map[int]interface{}
	exits     chan runtime.Process
	ooms      chan string
	epollFd   int
}

// Exits returns the channel used to notify of a process exit
func (m *Monitor) Exits() chan runtime.Process {
	return m.exits
}

// OOMs returns the channel used to notify of a container exit due to OOM
func (m *Monitor) OOMs() chan string {
	return m.ooms
}

// Monitor adds a process to the list of the one being monitored
func (m *Monitor) Monitor(p runtime.Process) error {
	m.m.Lock()
	defer m.m.Unlock()
	fd := p.ExitFD()
	if _, err := C.port_associate(C.int(m.epollFd), C.PORT_SOURCE_FD, C.uintptr_t(fd), C.POLLIN|C.POLLHUP, unsafe.Pointer(&fd)); err != nil {
		return err
	}
	EpollFdCounter.Inc(1)
	m.receivers[fd] = p
	return nil
}

// MonitorOOM adds a container to the list of the ones monitored for OOM
// There is no OOM-Killer on Solaris therefore nothing to setup
func (m *Monitor) MonitorOOM(c runtime.Container) error {
	return nil
}

// Close cleans up resources allocated by NewMonitor()
func (m *Monitor) Close() error {
	_, err := C.close(C.int(m.epollFd))
	return err
}

func (m *Monitor) start() {
	for {
		ev := C.getEvent(C.int(m.epollFd))
		if ev == nil {
			continue
		}
		fd := int(C.getFd(C.uintptr_t(uintptr((ev.portev_user)))))

		if fd < 0 {
			logrus.Warnf("containerd: epoll wait")
		}

		m.m.Lock()
		r := m.receivers[fd]
		switch t := r.(type) {
		case runtime.Process:
			if ev.portev_events == C.POLLHUP {
				delete(m.receivers, fd)
				if err := t.Close(); err != nil {
					logrus.Warnf("containerd: close process IO")
				}
				EpollFdCounter.Dec(1)
				m.exits <- t
			}
		case runtime.OOM:
			// always flush the event fd
			t.Flush()
			if t.Removed() {
				delete(m.receivers, fd)
				// epoll will remove the fd from its set after it has been closed
				t.Close()
				EpollFdCounter.Dec(1)
			} else {
				m.ooms <- t.ContainerID()
			}
		}
		m.m.Unlock()
		C.freeEvent(ev)
	}
}
