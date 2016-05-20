package subreaper

import (
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/Sirupsen/logrus"
	"github.com/docker/containerd/osutils"
)

var (
	subscriptions = map[int]*Subscription{}
	subLock       = sync.Mutex{}
	counter       = 0
	once          = sync.Once{}
)

type Subscription struct {
	id   int
	exit osutils.Exit
	c    chan osutils.Exit
	wg   sync.WaitGroup
}

func (s *Subscription) SetPid(pid int) {
	go func() {
		for exit := range s.c {
			if exit.Pid == pid {
				s.exit = exit
				s.wg.Done()
				Unsubscribe(s)
			}
		}
	}()
}

func (s *Subscription) Wait() int {
	s.wg.Wait()
	return s.exit.Status
}

func Subscribe() *Subscription {
	subLock.Lock()
	defer subLock.Unlock()

	Start()

	counter++
	s := &Subscription{
		id: counter,
		c:  make(chan osutils.Exit, 1024),
	}
	s.wg.Add(1)
	subscriptions[s.id] = s
	return s
}

func Unsubscribe(sub *Subscription) {
	subLock.Lock()
	defer subLock.Unlock()

	delete(subscriptions, sub.id)
}

func Start() error {
	var err error
	once.Do(func() {
		err = osutils.SetSubreaper(1)
		if err != nil {
			return
		}

		s := make(chan os.Signal, 2048)
		signal.Notify(s, syscall.SIGCHLD)
		go childReaper(s)
	})

	return err
}

func childReaper(s chan os.Signal) {
	for range s {
		exits, err := osutils.Reap()
		if err == nil {
			notify(exits)
		} else {
			logrus.WithField("error", err).Warn("containerd: reap child processes")
		}
	}
}

func notify(exits []osutils.Exit) {
	subLock.Lock()
	for _, exit := range exits {
		for _, sub := range subscriptions {
			sub.c <- exit
		}
	}
	subLock.Unlock()
}
