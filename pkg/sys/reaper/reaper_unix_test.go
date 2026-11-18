//go:build !windows

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

package reaper

import (
	"runtime"
	"testing"
	"time"

	runc "github.com/containerd/go-runc"
	"github.com/stretchr/testify/assert"
)

func TestWaitPid(t *testing.T) {
	for _, tc := range []struct {
		name string
		// events are sent in order: an exit, or the pid (Pid 0 with Status
		// set to the pid).
		events []runc.Exit
		// reaped reports false for its first notReaped calls.
		notReaped int
		want      runc.Exit
		ok        bool
	}{
		{
			name:   "exit after the pid is known",
			events: []runc.Exit{{Pid: 7, Status: 1}, {Status: 42}, {Pid: 8, Status: 2}, {Pid: 42, Status: 137}},
			want:   runc.Exit{Pid: 42, Status: 137},
			ok:     true,
		},
		{
			name:   "exit before the pid is known",
			events: []runc.Exit{{Pid: 42, Status: 137}, {Pid: 7, Status: 1}, {Status: 42}},
			want:   runc.Exit{Pid: 42, Status: 137},
			ok:     true,
		},
		{
			name:      "exit of an earlier process with the same pid",
			events:    []runc.Exit{{Pid: 42, Status: 1}, {Status: 42}, {Pid: 42, Status: 137}},
			notReaped: 1,
			want:      runc.Exit{Pid: 42, Status: 137},
			ok:        true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := &Monitor{subscribers: make(map[chan runc.Exit]*subscriber)}
			ec := m.Subscribe()
			pidCh := make(chan int)
			done := make(chan struct{})
			var (
				got runc.Exit
				ok  bool
			)
			calls := 0
			reaped := func() bool {
				calls++
				return calls > tc.notReaped
			}
			go func() {
				defer close(done)
				got, ok = m.WaitPid(ec, pidCh, reaped)
			}()
			for _, e := range tc.events {
				if e.Pid == 0 {
					// The exits sent so far are handled before the pid.
					for len(ec) > 0 {
						runtime.Gosched()
					}
					pidCh <- e.Status
				} else {
					ec <- e
				}
			}
			select {
			case <-done:
			case <-time.After(10 * time.Second):
				t.Fatal("WaitPid did not return")
			}
			assert.Equal(t, tc.ok, ok)
			assert.Equal(t, tc.want, got)
			assert.Empty(t, m.getSubscribers(), "the subscription is closed")
		})
	}

	t.Run("pid channel closed", func(t *testing.T) {
		m := &Monitor{subscribers: make(map[chan runc.Exit]*subscriber)}
		ec := m.Subscribe()
		pidCh := make(chan int)
		close(pidCh)
		_, ok := m.WaitPid(ec, pidCh, func() bool { return true })
		assert.False(t, ok)
		assert.Empty(t, m.getSubscribers())
	})
}
