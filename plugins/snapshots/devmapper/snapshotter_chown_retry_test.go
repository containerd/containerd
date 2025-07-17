//go:build linux

package devmapper

import (
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/containerd/containerd/v2/pkg/fstest"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestChownRetryMechanism(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test in short mode")
	}

	const loops = 5

	for i := 0; i < loops; i++ {
		t.Run(fmt.Sprintf("iteration_%d", i), func(t *testing.T) {
			root := t.TempDir()
			target := filepath.Join(root, "test_file")

			require.NoError(t, os.WriteFile(target, []byte("test content"), 0o644))

			originalChownFunc := fstest.ChownFunc
			defer func() {
				fstest.ChownFunc = originalChownFunc
			}()

			var failCount int32 = 2
			fstest.ChownFunc = func(path string, uid, gid int) error {
				remaining := atomic.AddInt32(&failCount, -1)
				if remaining >= 0 {
					return unix.EBUSY
				}
				return nil
			}

			err := fstest.Chown("test_file", 0, 0).Apply(root)
			require.NoError(t, err, "Chown should succeed after retries")
			require.True(t, atomic.LoadInt32(&failCount) < 0, "Should have exhausted fail count")
		})
	}
}
