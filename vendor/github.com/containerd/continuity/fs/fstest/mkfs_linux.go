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

package fstest

import (
	"os/exec"
	"testing"

	"github.com/containerd/continuity/testutil"
	"github.com/containerd/continuity/testutil/loopback"
)

func WithMkfs(t *testing.T, f func(), mkfs ...string) {
	testutil.RequiresRoot(t)
	mnt := t.TempDir()
	loop, err := loopback.New(100 << 20) // 100 MB
	if err != nil {
		t.Fatal(err)
	}
	defer loop.Close()
	if out, err := exec.Command(mkfs[0], append(mkfs[1:], loop.Device)...).CombinedOutput(); err != nil {
		t.Fatalf("could not mkfs (%v) %s: %v (out: %q)", mkfs, loop.Device, err, string(out))
	}
	if out, err := exec.Command("mount", loop.Device, mnt).CombinedOutput(); err != nil {
		t.Fatalf("could not mount %s: %v (out: %q)", loop.Device, err, string(out))
	}
	defer testutil.Unmount(t, mnt)
	t.Setenv("TMPDIR", mnt)
	f()
}
