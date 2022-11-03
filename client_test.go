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
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCheckRuntime(t *testing.T) {
	tests := []struct {
		current, prefix string
		out             bool
	}{
		{current: "", prefix: ".", out: false},
		{current: ".", prefix: "", out: true},
		{current: "....b.c.d", prefix: "", out: true},
		{current: "....b.c.d", prefix: ".", out: true},
		{current: "a.b.c.d", prefix: "a.b.c", out: true},
		{current: "a.b.c.d.", prefix: "a.b.c", out: true},
		{current: "a.b.c", prefix: "a.b.c.d", out: false},
		{current: "a.b.c", prefix: "a.b.c.d.", out: false},
		{current: "a.b.c.", prefix: "a.b.c", out: true},
		{current: "....", prefix: "....", out: true},
		{current: "....", prefix: ".", out: true},
		{current: "a....", prefix: "a", out: true},
		{current: "a.b...", prefix: "a", out: true},
		{current: "a..foo..bar...", prefix: "a", out: true},
		{current: "", prefix: "io.containerd", out: false},
		{current: "io.containerd", prefix: "", out: false},
		{current: "io.containerd", prefix: "io", out: true},
		{current: "io.containerd.runc", prefix: "io.containerd", out: true},
		{current: "io.containerd.runc.v1", prefix: "io.containerd", out: true},
		{current: "io.containerd", prefix: "io.containerd.runc", out: false},
		{current: "io.containerd", prefix: "io.containerd.runc.v1", out: false},
		{current: "io.containerdruncv1", prefix: "io.containerd", out: false},
		{current: "io.containerd", prefix: "io.containerd", out: true},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.current+"/"+tc.prefix, func(t *testing.T) {
			assert.Equal(t, tc.out, CheckRuntime(tc.current, tc.prefix))
		})
	}
}
