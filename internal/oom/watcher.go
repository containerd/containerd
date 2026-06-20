//go:build linux

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

package oom

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/containerd/errdefs"
	"golang.org/x/sys/unix"
)

func New() Interface {
	return &oomWatchers{
		watchers: make(map[string]*watcher),
	}
}

type oomWatchers struct {
	mu sync.Mutex

	watchers map[string]*watcher
}

func (ows *oomWatchers) Add(cid string, pid int, fn EventFunc) (retErr error) {
	cgroupPath, err := getCgroup2Path(pid)
	if err != nil {
		return fmt.Errorf("failed to get cgroupv2 path: %w", err)
	}

	eventFD, err := memoryEventNonBlockFD(cgroupPath)
	if err != nil {
		return fmt.Errorf("failed to get memory.events watch FD: %w", err)
	}
	defer func() {
		if retErr != nil {
			eventFD.Close()
		}
	}()

	ows.mu.Lock()
	defer ows.mu.Unlock()

	_, exist := ows.watchers[cid]
	if exist {
		return fmt.Errorf("failed to add oom watcher to container %s: %w", cid, errdefs.ErrAlreadyExists)
	}

	w := &watcher{
		cid:        cid,
		cgroupPath: cgroupPath,
		eventFD:    eventFD,
		eventFn:    fn,
		errCh:      make(chan error, 1),
	}
	w.start()

	ows.watchers[cid] = w
	return nil
}

func (ows *oomWatchers) Stop(cid string) error {
	ows.mu.Lock()
	w, exist := ows.watchers[cid]
	ows.mu.Unlock()

	if !exist {
		return nil
	}
	return w.stop()
}

type watcher struct {
	cid        string
	cgroupPath string

	eventFD *os.File
	eventFn EventFunc
	errCh   chan error
}

func (w *watcher) start() {
	go func() {
		defer close(w.errCh)
		defer w.eventFD.Close()

		// The inotify read buffer and the memory.events read buffer are
		// allocated once and reused for the lifetime of the watcher. This loop
		// is woken for every modification of memory.events (which the kernel
		// updates on every low/high/max reclaim event, not only on OOM kills),
		// so avoiding per-wakeup allocations keeps long-running shims with
		// stable workloads from accumulating CPU and GC overhead.
		// See https://github.com/containerd/containerd/issues/13558.
		var (
			oomKills   uint64
			shouldExit bool
			eventBuf   = make([]byte, unix.SizeofInotifyEvent*10)
			statsBuf   = make([]byte, memoryEventsBufSize)
		)
		for !shouldExit {
			bytesRead, err := w.eventFD.Read(eventBuf)
			if err != nil {
				if !errors.Is(err, os.ErrClosed) {
					w.errCh <- err
					return
				}
				shouldExit = true
			} else if bytesRead < unix.SizeofInotifyEvent {
				continue
			}

			oomKill, err := readMemoryOOMKill(w.cgroupPath, statsBuf)
			if err != nil {
				// When cgroup is deleted read may return -ENODEV instead of -ENOENT from open.
				if _, statErr := os.Lstat(filepath.Join(w.cgroupPath, "memory.events")); !os.IsNotExist(statErr) {
					w.errCh <- err
				}
				return
			}

			if oomKill > oomKills {
				oomKills = oomKill
				w.eventFn(w.cid)
			}
		}
	}()
}

func (w *watcher) stop() error {
	cerr := w.eventFD.Close()
	if errors.Is(cerr, os.ErrClosed) {
		cerr = nil
	}
	werr := <-w.errCh
	return errors.Join(cerr, werr)
}
