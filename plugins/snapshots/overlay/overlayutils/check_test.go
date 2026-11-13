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
	"errors"
	"os/exec"
	"testing"

	kernel "github.com/containerd/containerd/v2/pkg/kernelversion"
	"github.com/containerd/containerd/v2/pkg/testutil"
	"github.com/containerd/continuity/testutil/loopback"
)

func stubProbeUserXattr(t *testing.T, probe func(string) (bool, error)) {
	t.Helper()
	orig := probeUserXattr
	probeUserXattr = probe
	userXattrSupport.Clear()
	t.Cleanup(func() {
		probeUserXattr = orig
		userXattrSupport.Clear()
	})
}

func TestSupportsUserXattrCached(t *testing.T) {
	calls := 0
	stubProbeUserXattr(t, func(string) (bool, error) {
		calls++
		return true, nil
	})

	// Both directories are on the same filesystem.
	for _, d := range []string{t.TempDir(), t.TempDir()} {
		supported, err := supportsUserXattr(d)
		if err != nil {
			t.Fatal(err)
		}
		if !supported {
			t.Error("expected user xattrs to be supported")
		}
	}
	if calls != 1 {
		t.Errorf("expected the probe to run once, got %d", calls)
	}
}

func TestSupportsUserXattrErrorNotCached(t *testing.T) {
	calls := 0
	stubProbeUserXattr(t, func(string) (bool, error) {
		calls++
		if calls == 1 {
			return false, errors.New("probe failed")
		}
		return true, nil
	})

	d := t.TempDir()
	if _, err := supportsUserXattr(d); err == nil {
		t.Fatal("expected an error")
	}
	supported, err := supportsUserXattr(d)
	if err != nil {
		t.Fatal(err)
	}
	if !supported {
		t.Error("expected user xattrs to be supported")
	}
	if calls != 2 {
		t.Errorf("expected the probe to run twice, got %d", calls)
	}
}

// stubNeedsUserXAttrDeps makes NeedsUserXAttr behave as if it runs in a user namespace
// (or not) with the directory on tmpfs (or not), on a kernel >= 5.11.
func stubNeedsUserXAttrDeps(t *testing.T, inUserNS, onTmpfs bool, probe func(string) (bool, error)) {
	t.Helper()
	origUserNS, origTmpfs, origKernel := usernsRunningInUserNS, isPathOnTmpfs, kernelGreaterEqualThan
	usernsRunningInUserNS = func() bool { return inUserNS }
	isPathOnTmpfs = func(string) bool { return onTmpfs }
	kernelGreaterEqualThan = func(kernel.KernelVersion) (bool, error) { return true, nil }
	t.Cleanup(func() {
		usernsRunningInUserNS, isPathOnTmpfs, kernelGreaterEqualThan = origUserNS, origTmpfs, origKernel
	})
	stubProbeUserXattr(t, probe)
}

func TestNeedsUserXAttr(t *testing.T) {
	errProbe := errors.New("probe failed")

	for _, tc := range []struct {
		name        string
		inUserNS    bool
		onTmpfs     bool
		probe       func(string) (bool, error)
		expected    bool
		expectedErr error
		probeCalls  int
	}{
		{
			name:     "initial user namespace",
			inUserNS: false,
			onTmpfs:  true,
			expected: false,
		},
		{
			name:     "not on tmpfs",
			inUserNS: true,
			onTmpfs:  false,
			expected: true,
		},
		{
			name:       "tmpfs with user xattr support",
			inUserNS:   true,
			onTmpfs:    true,
			probe:      func(string) (bool, error) { return true, nil },
			expected:   true,
			probeCalls: 1,
		},
		{
			name:       "tmpfs without user xattr support",
			inUserNS:   true,
			onTmpfs:    true,
			probe:      func(string) (bool, error) { return false, nil },
			expected:   false,
			probeCalls: 1,
		},
		{
			name:        "tmpfs probe error",
			inUserNS:    true,
			onTmpfs:     true,
			probe:       func(string) (bool, error) { return false, errProbe },
			expected:    false,
			expectedErr: errProbe,
			probeCalls:  1,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			probeCalls := 0
			stubNeedsUserXAttrDeps(t, tc.inUserNS, tc.onTmpfs, func(d string) (bool, error) {
				probeCalls++
				if tc.probe == nil {
					t.Fatal("unexpected probe")
				}
				return tc.probe(d)
			})

			userxattr, err := NeedsUserXAttr(t.TempDir())
			if !errors.Is(err, tc.expectedErr) {
				t.Fatalf("expected error %v, got %v", tc.expectedErr, err)
			}
			if userxattr != tc.expected {
				t.Errorf("expected %v, got %v", tc.expected, userxattr)
			}
			if probeCalls != tc.probeCalls {
				t.Errorf("expected %d probe calls, got %d", tc.probeCalls, probeCalls)
			}
		})
	}
}

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
