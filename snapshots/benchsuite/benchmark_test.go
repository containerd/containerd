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

package benchsuite

import (
	"context"
	"crypto/rand"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/containerd/continuity/fs/fstest"
	"github.com/stretchr/testify/assert"

	"github.com/containerd/containerd/v2/mount"
	"github.com/containerd/containerd/v2/snapshots"
	"github.com/containerd/containerd/v2/snapshots/devmapper"
	"github.com/containerd/containerd/v2/snapshots/native"
	"github.com/containerd/containerd/v2/snapshots/overlay"
	"github.com/containerd/log"
)

var (
	dmPoolDev       string
	dmRootPath      string
	overlayRootPath string
	nativeRootPath  string
)

func init() {
	flag.StringVar(&dmPoolDev, "dm.thinPoolDev", "", "Pool device to run benchmark on")
	flag.StringVar(&dmRootPath, "dm.rootPath", "", "Root dir for devmapper snapshotter")
	flag.StringVar(&overlayRootPath, "overlay.rootPath", "", "Root dir for overlay snapshotter")
	flag.StringVar(&nativeRootPath, "native.rootPath", "", "Root dir for native snapshotter")

	// Avoid mixing benchmark output and INFO messages
	if err := log.SetLevel("error"); err != nil {
		panic(fmt.Sprintf("failed to set up log level: %v", err))
	}
}

func BenchmarkNative(b *testing.B) {
	if nativeRootPath == "" {
		b.Skip("native root dir must be provided")
	}

	snapshotter, err := native.NewSnapshotter(nativeRootPath)
	assert.Nil(b, err)

	defer func() {
		err = snapshotter.Close()
		assert.Nil(b, err)

		err = os.RemoveAll(nativeRootPath)
		assert.Nil(b, err)
	}()

	benchmarkSnapshotter(b, snapshotter)
}

func BenchmarkOverlay(b *testing.B) {
	if overlayRootPath == "" {
		b.Skip("overlay root dir must be provided")
	}

	snapshotter, err := overlay.NewSnapshotter(overlayRootPath)
	assert.Nil(b, err, "failed to create overlay snapshotter")

	defer func() {
		err = snapshotter.Close()
		assert.Nil(b, err)

		err = os.RemoveAll(overlayRootPath)
		assert.Nil(b, err)
	}()

	benchmarkSnapshotter(b, snapshotter)
}

func BenchmarkDeviceMapper(b *testing.B) {
	if dmPoolDev == "" {
		b.Skip("devmapper benchmark requires thin-pool device to be prepared in advance and provided")
	}

	if dmRootPath == "" {
		b.Skip("devmapper snapshotter root dir must be provided")
	}

	config := &devmapper.Config{
		PoolName:      dmPoolDev,
		RootPath:      dmRootPath,
		BaseImageSize: "16Mb",
	}

	ctx := context.Background()

	snapshotter, err := devmapper.NewSnapshotter(ctx, config)
	assert.Nil(b, err)

	defer func() {
		err := snapshotter.ResetPool(ctx)
		assert.Nil(b, err)

		err = snapshotter.Close()
		assert.Nil(b, err)

		err = os.RemoveAll(dmRootPath)
		assert.Nil(b, err)
	}()

	benchmarkSnapshotter(b, snapshotter)
}

// benchmarkSnapshotter tests snapshotter performance.
// It writes 16 layers with randomly created, modified, or removed files.
// Depending on layer index different sets of files are modified.
// In addition to total snapshotter execution time, benchmark outputs a few additional
// details - time taken to Prepare layer, mount, write data and unmount time,
// and Commit snapshot time.
func benchmarkSnapshotter(b *testing.B, snapshotter snapshots.Snapshotter) {
	const (
		layerCount    = 16
		fileSizeBytes = int64(1 * 1024 * 1024) // 1 MB
	)

	var (
		total      = 0
		layers     = make([]fstest.Applier, 0, layerCount)
		layerIndex = int64(0)
	)

	for i := 1; i <= layerCount; i++ {
		appliers := makeApplier(i, fileSizeBytes)
		layers = append(layers, fstest.Apply(appliers...))
		total += len(appliers)
	}

	var (
		benchN          int
		prepareDuration time.Duration
		writeDuration   time.Duration
		commitDuration  time.Duration
	)

	// Wrap test with Run so additional details output will be added right below the benchmark result
	b.Run("run", func(b *testing.B) {
		var (
			ctx     = context.Background()
			parent  string
			current string
		)

		// Reset durations since test might be ran multiple times
		prepareDuration = 0
		writeDuration = 0
		commitDuration = 0
		benchN = b.N

		b.SetBytes(int64(total) * fileSizeBytes)

		var timer time.Time
		for i := 0; i < b.N; i++ {
			for l := 0; l < layerCount; l++ {
				current = fmt.Sprintf("prepare-layer-%d", atomic.AddInt64(&layerIndex, 1))

				timer = time.Now()
				mounts, err := snapshotter.Prepare(ctx, current, parent)
				assert.Nil(b, err)
				prepareDuration += time.Since(timer)

				timer = time.Now()
				err = mount.WithTempMount(ctx, mounts, layers[l].Apply)
				assert.Nil(b, err)
				writeDuration += time.Since(timer)

				parent = fmt.Sprintf("committed-%d", atomic.AddInt64(&layerIndex, 1))

				timer = time.Now()
				err = snapshotter.Commit(ctx, parent, current)
				assert.Nil(b, err)
				commitDuration += time.Since(timer)
			}
		}
	})

	// Output extra measurements - total time taken to Prepare, mount and write data, and Commit
	const outputFormat = "%-25s\t%s\n"
	fmt.Fprintf(os.Stdout,
		outputFormat,
		b.Name()+"/prepare",
		testing.BenchmarkResult{N: benchN, T: prepareDuration})

	fmt.Fprintf(os.Stdout,
		outputFormat,
		b.Name()+"/write",
		testing.BenchmarkResult{N: benchN, T: writeDuration})

	fmt.Fprintf(os.Stdout,
		outputFormat,
		b.Name()+"/commit",
		testing.BenchmarkResult{N: benchN, T: commitDuration})

	fmt.Fprintln(os.Stdout)
}

// makeApplier returns a slice of fstest.Applier where files are written randomly.
// Depending on layer index, the returned layers will overwrite some files with the
// same generated names with new contents or deletions.
func makeApplier(layerIndex int, fileSizeBytes int64) []fstest.Applier {
	seed := time.Now().UnixNano()

	switch {
	case layerIndex%3 == 0:
		return []fstest.Applier{
			updateFile("/a"),
			updateFile("/b"),
			fstest.CreateRandomFile("/c", seed, fileSizeBytes, 0777),
			updateFile("/d"),
			fstest.CreateRandomFile("/f", seed, fileSizeBytes, 0777),
			updateFile("/e"),
			fstest.RemoveAll("/g"),
			fstest.CreateRandomFile("/h", seed, fileSizeBytes, 0777),
			updateFile("/i"),
			fstest.CreateRandomFile("/j", seed, fileSizeBytes, 0777),
		}
	case layerIndex%2 == 0:
		return []fstest.Applier{
			updateFile("/a"),
			fstest.CreateRandomFile("/b", seed, fileSizeBytes, 0777),
			fstest.RemoveAll("/c"),
			fstest.CreateRandomFile("/d", seed, fileSizeBytes, 0777),
			updateFile("/e"),
			fstest.RemoveAll("/f"),
			fstest.CreateRandomFile("/g", seed, fileSizeBytes, 0777),
			updateFile("/h"),
			fstest.CreateRandomFile("/i", seed, fileSizeBytes, 0777),
			updateFile("/j"),
		}
	default:
		return []fstest.Applier{
			fstest.CreateRandomFile("/a", seed, fileSizeBytes, 0777),
			fstest.CreateRandomFile("/b", seed, fileSizeBytes, 0777),
			fstest.CreateRandomFile("/c", seed, fileSizeBytes, 0777),
			fstest.CreateRandomFile("/d", seed, fileSizeBytes, 0777),
			fstest.CreateRandomFile("/e", seed, fileSizeBytes, 0777),
			fstest.CreateRandomFile("/f", seed, fileSizeBytes, 0777),
			fstest.CreateRandomFile("/g", seed, fileSizeBytes, 0777),
			fstest.CreateRandomFile("/h", seed, fileSizeBytes, 0777),
			fstest.CreateRandomFile("/i", seed, fileSizeBytes, 0777),
			fstest.CreateRandomFile("/j", seed, fileSizeBytes, 0777),
		}
	}
}

// applierFn represents helper func that implements fstest.Applier
type applierFn func(root string) error

func (fn applierFn) Apply(root string) error {
	return fn(root)
}

// updateFile modifies a few bytes in the middle in order to demonstrate the difference in performance
// for block-based snapshotters (like devicemapper) against file-based snapshotters (like overlay, which need to
// perform a copy-up of the full file any time a single bit is modified).
func updateFile(name string) applierFn {
	return func(root string) error {
		path := filepath.Join(root, name)
		file, err := os.OpenFile(path, os.O_WRONLY, 0600)
		if err != nil {
			return fmt.Errorf("failed to open %q: %w", path, err)
		}

		info, err := file.Stat()
		if err != nil {
			file.Close()
			return err
		}

		var (
			offset = info.Size() / 2
			buf    = make([]byte, 4)
		)

		if _, err := rand.Read(buf); err != nil {
			file.Close()
			return err
		}

		if _, err := file.WriteAt(buf, offset); err != nil {
			file.Close()
			return fmt.Errorf("failed to write %q at offset %d: %w", path, offset, err)
		}

		return file.Close()
	}
}
