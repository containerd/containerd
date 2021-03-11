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

package containerd

import (
	"fmt"
	"syscall"
	"testing"
)

func TestParsePlatformSignalOnWindows(t *testing.T) {
	testSignals := []struct {
		raw      string
		want     syscall.Signal
		platform string
		err      bool
	}{
		// test signals for linux platform
		{"1", syscall.Signal(1), "linux", false},
		{"SIGKILL", syscall.SIGKILL, "linux", false},
		{"NONEXIST", 0, "linux", true},
		{"65536", syscall.Signal(65536), "linux", false},

		// test signals for windows platform
		{"1", syscall.Signal(1), "windows", false},
		{"SIGKILL", syscall.SIGKILL, "windows", false},
		{"NONEXIST", 0, "windows", true},
		{"65536", 0, "windows", true},

		// make sure signals from opposing platform do not resolve
		{"SIGWINCH", 0, "windows", true},
		{"SIGWINCH", syscall.Signal(0x1c), "linux", false},
	}
	for _, ts := range testSignals {
		t.Run(fmt.Sprintf("%s/%d/%s/%t", ts.raw, ts.want, ts.platform, ts.err), func(t *testing.T) {
			got, err := ParsePlatformSignal(ts.raw, ts.platform)
			if ts.err && err == nil {
				t.Errorf("ParsePlatformSignal(%s) should return error", ts.raw)
			}
			if !ts.err && got != ts.want {
				t.Errorf("ParsePlatformSignal(%s) return %d, want %d", ts.raw, got, ts.want)
			}
		})
	}
}
