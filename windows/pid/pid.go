// +build windows

package pid

import (
	"errors"
	"sync"
)

type Pool struct {
	sync.Mutex
	pool map[uint32]struct{}
	cur  uint32
}

func NewPool() *Pool {
	return &Pool{
		pool: make(map[uint32]struct{}),
	}
}

func (p *Pool) Get() (uint32, error) {
	p.Lock()
	defer p.Unlock()

	pid := p.cur + 1
	for pid != p.cur {
		// 0 is reserved and invalid
		if pid == 0 {
			pid = 1
		}
		if _, ok := p.pool[pid]; !ok {
			p.cur = pid
			return pid, nil
		}
		pid++
	}

	return 0, errors.New("pid pool exhausted")
}

func (p *Pool) Put(pid uint32) {
	p.Lock()
	delete(p.pool, pid)
	p.Unlock()
}
