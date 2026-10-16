//go:build !windows

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
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	. "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/containerd/containerd/v2/pkg/oci"
	"github.com/containerd/platforms"
	"github.com/stretchr/testify/require"
)

func TestImageCorruptionRecovery(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}

	client, err := newClient(t, address)
	require.NoError(t, err)
	defer client.Close()

	ctx, cancel := testContext(t)
	defer cancel()

	imageName := testMultiLayeredImage
	_ = client.ImageService().Delete(ctx, imageName, images.SynchronousDelete())

	image, err := client.Pull(ctx, imageName, WithPlatformMatcher(platforms.Default()))
	require.NoError(t, err)
	defer client.ImageService().Delete(ctx, imageName, images.SynchronousDelete())

	manifest, err := images.Manifest(ctx, client.ContentStore(), image.Target(), platforms.Default())
	require.NoError(t, err)
	require.NotEmpty(t, manifest.Layers)

	layer := manifest.Layers[0]
	require.NoError(t, images.VerifyDescriptor(ctx, client.ContentStore(), layer))

	blob := filepath.Join(defaultRoot, "io.containerd.content.v1.content", "blobs",
		layer.Digest.Algorithm().String(), layer.Digest.Encoded())
	original, err := os.ReadFile(blob)
	require.NoError(t, err)
	corrupt, err := os.CreateTemp(filepath.Dir(blob), "corrupt-")
	require.NoError(t, err)
	_, err = corrupt.WriteAt(original, 0)
	require.NoError(t, err)
	_, err = corrupt.WriteAt([]byte("trailing corruption"), layer.Size)
	require.NoError(t, err)
	require.NoError(t, corrupt.Close())
	require.NoError(t, os.Rename(corrupt.Name(), blob))

	err = image.Unpack(ctx, testSnapshotter)
	require.Error(t, err)

	require.ErrorIs(t, images.VerifyDescriptor(ctx, client.ContentStore(), layer), images.ErrContentMismatch)

	// Delete the image synchronously so the damaged shared blob is collected
	// before pulling it again.
	require.NoError(t, client.ImageService().Delete(ctx, imageName, images.SynchronousDelete()))

	image, err = client.Pull(ctx, imageName,
		WithPlatformMatcher(platforms.Default()), WithPullUnpack)
	require.NoError(t, err)
	require.NoError(t, images.VerifyDescriptor(ctx, client.ContentStore(), layer))

	container, err := client.NewContainer(ctx, t.Name(),
		WithNewSnapshot(t.Name(), image),
		WithNewSpec(oci.WithImageConfig(image), withExitStatus(0)))
	require.NoError(t, err)
	defer container.Delete(ctx, WithSnapshotCleanup)

	task, err := container.NewTask(ctx, empty())
	require.NoError(t, err)
	defer task.Delete(ctx)

	statusC, err := task.Wait(ctx)
	require.NoError(t, err)
	require.NoError(t, task.Start(ctx))

	status := <-statusC
	exitCode, _, err := status.Result()
	require.NoError(t, err)
	require.Equal(t, uint32(0), exitCode)
}

func TestImageCorruptionSurvivesCrashRestart(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}

	client, err := newClient(t, address)
	require.NoError(t, err)

	baseCtx, cancel := testContext(t)
	defer cancel()
	namespace := "corruption-restart-" + strings.ReplaceAll(t.Name(), "/", "-")
	require.NoError(t, client.NamespaceService().Create(baseCtx, namespace, nil))
	defer client.NamespaceService().Delete(baseCtx, namespace)
	ctx := namespaces.WithNamespace(baseCtx, namespace)

	image, err := client.Pull(ctx, testMultiLayeredImage, WithPlatformMatcher(platforms.Default()))
	require.NoError(t, err)
	manifest, err := images.Manifest(ctx, client.ContentStore(), image.Target(), platforms.Default())
	require.NoError(t, err)
	require.NotEmpty(t, manifest.Layers)
	layer := manifest.Layers[0]

	corruptLayer := func() error {
		blob := filepath.Join(defaultRoot, "io.containerd.content.v1.content", "blobs",
			layer.Digest.Algorithm().String(), layer.Digest.Encoded())
		original, err := os.ReadFile(blob)
		if err != nil {
			return err
		}
		corrupt, err := os.CreateTemp(filepath.Dir(blob), "corrupt-")
		if err != nil {
			return err
		}
		if _, err := corrupt.WriteAt(original, 0); err != nil {
			corrupt.Close()
			os.Remove(corrupt.Name())
			return err
		}
		if _, err := corrupt.WriteAt([]byte("trailing corruption"), layer.Size); err != nil {
			corrupt.Close()
			os.Remove(corrupt.Name())
			return err
		}
		if err := corrupt.Close(); err != nil {
			os.Remove(corrupt.Name())
			return err
		}
		return os.Rename(corrupt.Name(), blob)
	}

	require.NoError(t, client.Close())
	require.NoError(t, ctrd.CrashRestart(corruptLayer))

	waitCtx, waitCancel := context.WithTimeout(context.Background(), 10*time.Second)
	restarted, err := ctrd.waitForStart(waitCtx)
	waitCancel()
	require.NoError(t, err)
	defer restarted.Close()

	ctx = namespaces.WithNamespace(ctx, namespace)
	require.ErrorIs(t, images.VerifyDescriptor(ctx, restarted.ContentStore(), layer), images.ErrContentMismatch)
}
