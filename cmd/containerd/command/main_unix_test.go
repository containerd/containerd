//go:build linux || darwin || freebsd || solaris

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

package command

import (
	"context"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/containerd/containerd/v2/cmd/containerd/server"
	"golang.org/x/sys/unix"
)

func TestHandleSignalsIgnoreSIGHUP(t *testing.T) {
	var handlesSIGHUP bool
	for _, signal := range handledSignals {
		if signal == unix.SIGHUP {
			handlesSIGHUP = true
			break
		}
	}
	if !handlesSIGHUP {
		t.Fatal("handledSignals should include SIGHUP")
	}

	signals := make(chan os.Signal, 2)
	serverC := make(chan *server.Server, 1)
	var cancelCalled atomic.Bool
	cancel := func() {
		cancelCalled.Store(true)
	}

	done := handleSignals(context.Background(), signals, serverC, cancel)

	signals <- unix.SIGHUP

	select {
	case <-done:
		t.Fatal("signal handler exited on SIGHUP")
	case <-time.After(100 * time.Millisecond):
	}

	if cancelCalled.Load() {
		t.Fatal("cancel should not be called for SIGHUP")
	}

	signals <- unix.SIGTERM

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("signal handler did not exit on SIGTERM")
	}

	if !cancelCalled.Load() {
		t.Fatal("cancel should be called for SIGTERM")
	}
}
