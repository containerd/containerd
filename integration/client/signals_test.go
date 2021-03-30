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

package client

import (
	"fmt"
	"syscall"
	"testing"

	. "github.com/containerd/containerd"
)

func TestParseSignal(t *testing.T) {
	testSignals := []struct {
		raw  string
		want syscall.Signal
		err  bool
	}{
		{"1", syscall.Signal(1), false},
		{"SIGKILL", syscall.SIGKILL, false},
		{"NONEXIST", 0, true},
	}
	for _, ts := range testSignals {
		t.Run(fmt.Sprintf("%s/%d/%t", ts.raw, ts.want, ts.err), func(t *testing.T) {
			got, err := ParseSignal(ts.raw)
			if ts.err && err == nil {
				t.Errorf("ParseSignal(%s) should return error", ts.raw)
			}
			if !ts.err && got != ts.want {
				t.Errorf("ParseSignal(%s) return %d, want %d", ts.raw, got, ts.want)
			}
		})
	}
}
