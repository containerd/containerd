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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"

	"github.com/opencontainers/image-spec/identity"
	v1 "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/containerd/errdefs"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/containers"
	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/containerd/v2/defaults"
	"github.com/containerd/containerd/v2/pkg/oci"
)

func TestMountManager(t *testing.T) {
	client, err := newClient(t, address)
	require.NoError(t, err)
	defer client.Close()

	var (
		ctx, cancel = testContext(t)
	)
	defer cancel()

	// Test MountManager functionality
	mounts, err := client.MountManager().List(ctx)
	require.NoError(t, err)

	assert.Empty(t, mounts, "Mounts should be empty")

}

func TestMountAtRuntime(t *testing.T) {
	client, err := newClient(t, address)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	var (
		ctx, cancel = testContext(t)
		id1         = fmt.Sprintf("%s-1", t.Name())
		id2         = fmt.Sprintf("%s-2", t.Name())
		testDev     = createImgFile(t, "test-dev.img")
		workDir     = filepath.Join(t.TempDir(), "work")
	)
	defer cancel()

	require.NoError(t, os.MkdirAll(workDir, 0755))

	ctx, done, err := client.WithLease(ctx)
	require.NoError(t, err)
	defer done(ctx)

	img, err := client.GetImage(ctx, testImage)
	require.NoError(t, err)

	snapshotter := client.SnapshotService(defaults.DefaultSnapshotter)
	diffIDs, err := img.RootFS(ctx)
	require.NoError(t, err)

	mnt, err := snapshotter.View(ctx, id1, identity.ChainID(diffIDs).String())
	require.NoError(t, err)
	if len(mnt) > 1 {
		t.Skipf("Test can only be run with single mount")
	}
	m := append(mnt, mount.Mount{
		Type:    "xfs",
		Source:  testDev,
		Options: []string{"loop"},
	}, mount.Mount{
		Type:    "format/overlay",
		Source:  "overlay",
		Options: []string{"lowerdir={{ mount 0 }}", "upperdir={{ mount 1 }}/root1", "workdir={{ mount 1 }}/work", "index=off"},
	})

	container1, err := client.NewContainer(ctx, id1, containerd.WithNewSpec(withImage(img, "sh", "-c", `echo -n 'Test mount at runtime'> /etc/test-name.txt`)))
	require.NoError(t, err)
	defer func() { assert.NoError(t, container1.Delete(ctx)) }()

	task, err := container1.NewTask(ctx, empty(), containerd.WithRootFS(m))
	require.NoError(t, err)
	defer func() {
		if task != nil {
			task.Delete(ctx)
		}
	}()

	info, err := client.MountManager().Info(ctx, id1)
	require.NoError(t, err)
	require.Equal(t, 2, len(info.Active), "Expected two mounts for container")

	statusC, err := task.Wait(ctx)
	require.NoError(t, err)
	require.NoError(t, task.Start(ctx))
	<-statusC
	_, err = task.Delete(ctx)
	task = nil
	require.NoError(t, err)

	_, err = client.MountManager().Info(ctx, id1)
	require.ErrorIs(t, err, errdefs.ErrNotFound, "Expected not found error")

	// Start another container with results created from first container

	container2, err := client.NewContainer(ctx, id2, containerd.WithNewSpec(withImage(img, "cat", "/etc/test-name.txt")))
	require.NoError(t, err)
	defer func() { assert.NoError(t, container2.Delete(ctx)) }()

	m = append(mnt, mount.Mount{
		Type:    "xfs",
		Source:  testDev,
		Options: []string{"loop"},
	}, mount.Mount{
		Type:    "format/overlay",
		Source:  "overlay",
		Options: []string{"lowerdir={{ mount 1 }}/root1:{{ mount 0 }}", "upperdir={{ mount 1 }}/root2", "workdir={{ mount 1 }}/work", "index=off"},
	})

	direct, err := newDirectIO(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	defer direct.Delete()
	var (
		wg  sync.WaitGroup
		buf = bytes.NewBuffer(nil)
	)
	wg.Add(1)
	go func() {
		defer wg.Done()
		io.Copy(buf, direct.Stdout)
	}()

	task, err = container2.NewTask(ctx, direct.IOCreate, containerd.WithRootFS(m))
	require.NoError(t, err)
	defer func() {
		if task != nil {
			task.Delete(ctx)
		}
	}()

	info, err = client.MountManager().Info(ctx, id2)
	require.NoError(t, err)
	require.Equal(t, 2, len(info.Active), "Expected two mounts for container")

	statusC, err = task.Wait(ctx)
	require.NoError(t, err)
	require.NoError(t, task.Start(ctx))
	<-statusC
	_, err = task.Delete(ctx)
	task = nil
	require.NoError(t, err)

	wg.Wait()

	require.Equal(t, "Test mount at runtime", buf.String(), "Expected output from mounted file")
}

func createImgFile(t *testing.T, name string) string {
	mkfs, err := exec.LookPath("mkfs.xfs")
	if err != nil {
		t.Skipf("Could not find mkfs.xfs: %v", err)
	}

	loopbackSize := int64(300 << 20) // 300 MB

	f := filepath.Join(t.TempDir(), name)
	devFile, err := os.OpenFile(f, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
	require.NoError(t, err)

	if err := devFile.Truncate(loopbackSize); err != nil {
		devFile.Close()
		t.Fatalf("failed to resize %s file: %v", f, err)
	}

	if err := devFile.Sync(); err != nil {
		devFile.Close()
		t.Fatalf("failed to sync %s file: %v", f, err)
	}
	devFile.Close()

	if out, err := exec.Command(mkfs, f).CombinedOutput(); err != nil {
		t.Fatalf("failed to make xfs filesystem (out: %q): %v", out, err)
	}

	setupMount(t, f)

	return f
}

func setupMount(t *testing.T, f string) {
	root, err := os.MkdirTemp(t.TempDir(), "")
	require.NoError(t, err)
	defer os.RemoveAll(root)

	m := []mount.Mount{
		{
			Type:    "xfs",
			Source:  f,
			Options: []string{"loop", "sync"},
		},
	}

	require.NoError(t, mount.All(m, root))
	require.NoError(t, os.Mkdir(filepath.Join(root, "root1"), 0755))
	require.NoError(t, os.Mkdir(filepath.Join(root, "root2"), 0755))
	require.NoError(t, os.Mkdir(filepath.Join(root, "work"), 0755))
	require.NoError(t, mount.UnmountAll(root, 0))
}

func withImage(image containerd.Image, args ...string) oci.SpecOpts {
	return func(ctx context.Context, _ oci.Client, _ *containers.Container, s *oci.Spec) error {
		ic, err := image.Config(ctx)
		if err != nil {
			return err
		}
		if !images.IsConfigType(ic.MediaType) {
			return fmt.Errorf("unknown image config media type %s", ic.MediaType)
		}

		var (
			imageConfigBytes []byte
			ociimage         v1.Image
		)
		imageConfigBytes, err = content.ReadBlob(ctx, image.ContentStore(), ic)
		if err != nil {
			return err
		}

		if err = json.Unmarshal(imageConfigBytes, &ociimage); err != nil {
			return err
		}
		if s.Linux == nil {
			return fmt.Errorf("linux image section missing")
		}

		s.Process = &specs.Process{}
		s.Process.Env = ociimage.Config.Env
		s.Process.Args = append(ociimage.Config.Entrypoint, args...)

		cwd := ociimage.Config.WorkingDir
		if cwd == "" {
			cwd = "/"
		}
		s.Process.Cwd = cwd

		return nil
	}
}
