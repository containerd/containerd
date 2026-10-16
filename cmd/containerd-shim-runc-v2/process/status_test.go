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

package process

import (
	"context"
	"testing"
	"time"
)

// accessorsReturnPromptly asserts that the read-only status accessors of a
// process answer while the process's mu is held by a Delete in flight: draining
// stdio, exec'ing runc delete, closing the IO and unmounting the rootfs can
// each take seconds, and the task State RPC has a two second deadline.
// See https://github.com/containerd/containerd/issues/14379.
func accessorsReturnPromptly(t *testing.T, status func(context.Context) (string, error), exitStatus func() int, exitedAt func() time.Time) {
	t.Helper()

	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, err := status(context.Background()); err != nil {
			t.Errorf("Status returned error: %v", err)
		}
		_ = exitStatus()
		_ = exitedAt()
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("status accessors blocked while the process mutex was held")
	}
}

// statusDoesNotReturnEarly asserts that Status does not answer while the
// process's mu is held by a non-Delete lifecycle operation.
func statusDoesNotReturnEarly(t *testing.T, status func(context.Context) (string, error)) {
	t.Helper()

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = status(context.Background())
	}()

	select {
	case <-done:
		t.Fatal("Status returned while a lifecycle operation held mu")
	case <-time.After(100 * time.Millisecond):
	}

	// The caller releases mu; the pending Status must then complete.
	t.Cleanup(func() { <-done })
}

func TestInitStatusAccessorsRemainResponsiveDuringDelete(t *testing.T) {
	p := &Init{}
	p.setInitState(&createdState{p: p})

	// A Delete in flight announces itself before taking mu, which it then holds
	// across its slow work.
	p.deleting.Store(true)
	defer p.deleting.Store(false)
	p.mu.Lock()
	accessorsReturnPromptly(t, p.Status, p.ExitStatus, p.ExitedAt)
	p.mu.Unlock()
}

func TestExecStatusAccessorsRemainResponsiveDuringDelete(t *testing.T) {
	parent := &Init{}
	parent.setInitState(&createdState{p: parent})

	e := &execProcess{parent: parent}
	e.setExecState(&execCreatedState{p: e})

	// A Delete in flight announces itself before taking mu, which it then holds
	// across its slow work.
	e.deleting.Store(true)
	defer e.deleting.Store(false)
	e.mu.Lock()
	accessorsReturnPromptly(t, e.Status, e.ExitStatus, e.ExitedAt)
	e.mu.Unlock()
}

// TestInitStatusSerializesWithInFlightLifecycleOperations pins the boundary of
// the Delete exemption: Status may only skip mu while a Delete is cleaning up.
// While any other operation holds mu — a Start publishing `running` only after
// the runtime start returns, or a SetExited whose transition follows the wait
// notification — Status must keep waiting, or State could report a stale state
// to a recovery pass or reject a Delete that follows a Wait.
func TestInitStatusSerializesWithInFlightLifecycleOperations(t *testing.T) {
	p := &Init{}
	p.setInitState(&createdState{p: p})

	p.mu.Lock()
	statusDoesNotReturnEarly(t, p.Status)
	p.mu.Unlock()
}

func TestExecStatusSerializesWithInFlightLifecycleOperations(t *testing.T) {
	parent := &Init{}
	parent.setInitState(&createdState{p: parent})

	e := &execProcess{parent: parent}
	e.setExecState(&execCreatedState{p: e})

	e.mu.Lock()
	statusDoesNotReturnEarly(t, e.Status)
	e.mu.Unlock()
}
