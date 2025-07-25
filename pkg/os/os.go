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

package os

import (
	"context"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/moby/sys/symlink"

	"github.com/containerd/containerd/v2/core/mount"
)

// OS collects system level operations that need to be mocked out
// during tests.
type OS interface {
	MkdirAll(path string, perm os.FileMode) error
	RemoveAll(path string) error
	Stat(name string) (os.FileInfo, error)
	ResolveSymbolicLink(name string) (string, error)
	FollowSymlinkInScope(path, scope string) (string, error)
	CopyFile(src, dest string, perm os.FileMode) error
	WriteFile(filename string, data []byte, perm os.FileMode) error
	Hostname() (string, error)
	Mount(source string, target string, fstype string, flags uintptr, data string) error
	Unmount(target string) error
	LookupMount(path string) (mount.Info, error)
}

// RealOS is used to dispatch the real system level operations.
type RealOS struct{}

// MkdirAll will call os.MkdirAll to create a directory.
func (RealOS) MkdirAll(path string, perm os.FileMode) error {
	return os.MkdirAll(path, perm)
}

// RemoveAll will call os.RemoveAll to remove the path and its children.
func (RealOS) RemoveAll(path string) error {
	return os.RemoveAll(path)
}

// Stat will call os.Stat to get the status of the given file.
func (RealOS) Stat(name string) (os.FileInfo, error) {
	return os.Stat(name)
}

// FollowSymlinkInScope will call symlink.FollowSymlinkInScope.
func (RealOS) FollowSymlinkInScope(path, scope string) (string, error) {
	return symlink.FollowSymlinkInScope(path, scope)
}

// CopyFile will copy src file to dest file
func (RealOS) CopyFile(src, dest string, perm os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}

// WriteFile will call os.WriteFile to write data into a file.
func (RealOS) WriteFile(filename string, data []byte, perm os.FileMode) error {
	return os.WriteFile(filename, data, perm)
}

// Hostname will call os.Hostname to get the hostname of the host.
func (RealOS) Hostname() (string, error) {
	return os.Hostname()
}

// InflightStat represents a Stat call that is in progress for a given path.
// It is exported so that consumers of the OS interface can use it to coordinate.
type InflightStat struct {
	Cond *sync.Cond // Used to signal completion to waiting goroutines.
	Done bool       // Flag indicating if the Stat call is complete.
	Err  error      // Resulting error from Stat.
}

// StatManager wraps an OS interface to provide managed access to Stat calls,
// specifically to handle in-flight requests gracefully.
type StatManager struct {
	os                 OS
	statTimeout        time.Duration
	inflightStats      map[string]*InflightStat
	inflightStatsMutex sync.Mutex
}

type StatResult struct {
	Err error
}

// NewStatManager creates a new StatManager that wraps the provided OS interface.
func NewStatManager(os OS) *StatManager {
	return &StatManager{
		os:                 os,
		statTimeout:        5 * time.Second,
		inflightStats:      make(map[string]*InflightStat),
		inflightStatsMutex: sync.Mutex{},
	}
}

// Stat performs a stat on the given name, managing concurrent requests to the same path.
func (sm *StatManager) Stat(ctx context.Context, path string) error {
	sm.inflightStatsMutex.Lock()
	var statError error
	inflight, isPending := sm.inflightStats[path]
	if isPending {
		// Stat for this path is already in progress. We wait on the
		// condition variable. Wait() atomically unlocks the mutex and
		// suspends the goroutine. When awakened, it re-locks the mutex
		// before returning.
		for !inflight.Done {
			inflight.Cond.Wait()
		}
		statError = inflight.Err
		sm.inflightStatsMutex.Unlock()
	} else {
		// There are no active Stats being made to this path. Create a
		// record for other goroutines to find and wait on.
		newInflight := &InflightStat{
			Cond: sync.NewCond(&sm.inflightStatsMutex),
		}
		sm.inflightStats[path] = newInflight
		sm.inflightStatsMutex.Unlock()

		statCtx, statcancel := context.WithTimeout(ctx, sm.statTimeout)
		resultChan := make(chan StatResult, 1)
		go func() {
			_, err := sm.os.Stat(path)
			resultChan <- StatResult{Err: err}
			close(resultChan)
		}()

		select {
		case res := <-resultChan: // Stat completed
			statError = res.Err
		case <-statCtx.Done(): // timeout / mountpath could be stuck.
			statError = fmt.Errorf("mountpath (%q) could be stuck", path)
		}
		statcancel()

		sm.inflightStatsMutex.Lock()
		newInflight.Err = statError
		newInflight.Done = true

		// Delete the src entry from the map so that subsequent requests
		// can still call a new Stat to the src in case the underlying FS
		// has recovered and can serve files.
		delete(sm.inflightStats, path)

		newInflight.Cond.Broadcast()
		sm.inflightStatsMutex.Unlock()
	}
	return statError
}
