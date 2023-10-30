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

package overlayutils

import (
	"testing"

	"github.com/containerd/containerd/v2/pkg/testutil"
	"github.com/containerd/continuity/testutil/loopback"
	exec "golang.org/x/sys/execabs"
)

func testOverlaySupported(t testing.TB, expected bool, mkfs ...string) {
	testutil.RequiresRoot(t)
	mnt := t.TempDir()

	loop, err := loopback.New(100 << 20) // 100 MB
	if err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(mkfs[0], append(mkfs[1:], loop.Device)...).CombinedOutput(); err != nil {
		// not fatal
		loop.Close()
		t.Skipf("could not mkfs (%v) %s: %v (out: %q)", mkfs, loop.Device, err, string(out))
	}
	if out, err := exec.Command("mount", loop.Device, mnt).CombinedOutput(); err != nil {
		// not fatal
		loop.Close()
		t.Skipf("could not mount %s: %v (out: %q)", loop.Device, err, string(out))
	}
	defer func() {
		testutil.Unmount(t, mnt)
		loop.Close()
	}()
	workload := func() {
		err = Supported(mnt)
		if expected && err != nil {
			t.Fatal(err)
		}
		if !expected && err == nil {
			t.Fatal("error is expected")
		}
	}
	b, ok := t.(*testing.B)
	if ok {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			workload()
		}
		b.StopTimer()
	} else {
		workload()
	}
}

func BenchmarkOverlaySupportedOnExt4(b *testing.B) {
	testOverlaySupported(b, true, "mkfs.ext4", "-F")
}

func BenchmarkOverlayUnsupportedOnFType0XFS(b *testing.B) {
	testOverlaySupported(b, false, "mkfs.xfs", "-m", "crc=0", "-n", "ftype=0")
}

func BenchmarkOverlaySupportedOnFType1XFS(b *testing.B) {
	testOverlaySupported(b, true, "mkfs.xfs", "-m", "crc=0", "-n", "ftype=1")
}

func BenchmarkOverlayUnsupportedOnFAT(b *testing.B) {
	testOverlaySupported(b, false, "mkfs.fat")
}
