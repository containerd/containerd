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

// TestChownRetryMechanism verifies that fstest.Chown() will retry and eventually succeed
// after transient EBUSY errors. This test runs multiple iterations to ensure stability.
func TestChownRetryMechanism(t *testing.T) {
	const loops = 100 // Reduced for CI performance

	for i := 0; i < loops; i++ {
		t.Run(fmt.Sprintf("iteration_%d", i), func(t *testing.T) {
			root := t.TempDir()

			// Create target file
			target := filepath.Join(root, "test_file")
			require.NoError(t, os.WriteFile(target, []byte("test content"), 0o644))

			// Mock ChownFunc: first 2 calls return EBUSY, then succeed
			var failCount int32 = 2
			originalChownFunc := fstest.ChownFunc

			fstest.ChownFunc = func(path string, uid, gid int) error {
				if atomic.AddInt32(&failCount, -1) >= 0 {
					return unix.EBUSY
				}
				return originalChownFunc(path, uid, gid)
			}

			// Restore original function
			defer func() {
				fstest.ChownFunc = originalChownFunc
			}()

			// Test the retry mechanism
			err := fstest.Chown("test_file", 0, 0).Apply(root)
			require.NoError(t, err, "Chown should succeed after retries")
		})
	}
}
