// +build windows

/*
   Copyright The containerd Authors.

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.
*/

package windows

import (
	"errors"
	"sync"
)

type pidPool struct {
	sync.Mutex
	pool map[uint32]struct{}
	cur  uint32
}

func newPidPool() *pidPool {
	return &pidPool{
		pool: make(map[uint32]struct{}),
	}
}

func (p *pidPool) Get() (uint32, error) {
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
			p.pool[pid] = struct{}{}
			return pid, nil
		}
		pid++
	}

	return 0, errors.New("pid pool exhausted")
}

func (p *pidPool) Put(pid uint32) {
	p.Lock()
	delete(p.pool, pid)
	p.Unlock()
}
