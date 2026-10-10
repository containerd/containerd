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

package integration

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/containerd/errdefs"
	"github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/identity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	criruntime "k8s.io/cri-api/pkg/apis/runtime/v1"

	containerd "github.com/containerd/containerd/v2/client"
	containerdimages "github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/integration/images"
	"github.com/containerd/containerd/v2/plugins"
	"github.com/containerd/platforms"
)

// TestImageStatusDiscardedLayersSnapshotterSwitch reproduces the problem where
// an image pulled with discard_unpacked_layers=true becomes unusable for a
// snapshotter other than the one it was originally unpacked into, yet is still
// reported as present by ImageStatus.
//
// With discard_unpacked_layers=true, the layer blobs are dropped from the
// content store after the image is unpacked into the pull-time snapshotter
// (here, overlayfs). If a container is later created with a runtime handler
// backed by a different snapshotter (here, native), CreateContainer must unpack
// the image into that snapshotter — but it has no fetcher, so with the blobs
// gone the unpack is impossible. Before the fix, ImageStatus still reported the
// image present, so kubelet (which holds the pull credentials) never re-pulled
// it and container creation failed.
//
// The fix makes ImageStatus consult the snapshotter that the *queried* runtime
// handler would use: the image is reported absent for the native handler (no
// snapshot, blobs gone) while remaining present for the default overlayfs
// handler (snapshot intact). Re-pulling — what kubelet does in response to the
// absent status — then restores the discarded layer blobs to the content
// store, healing the image (the native unpack itself happens later, when a
// native container is created).
func TestImageStatusDiscardedLayersSnapshotterSwitch(t *testing.T) {
	workDir := t.TempDir()
	cfgPath := filepath.Join(workDir, "config.toml")
	cfg := `
version = 3

[plugins.'io.containerd.cri.v1.images']
  snapshotter = "overlayfs"
  discard_unpacked_layers = true

[plugins.'io.containerd.cri.v1.runtime'.containerd]
  default_runtime_name = "runc"

[plugins.'io.containerd.cri.v1.runtime'.containerd.runtimes.runc]
  runtime_type = "io.containerd.runc.v2"
  snapshotter = "overlayfs"

[plugins.'io.containerd.cri.v1.runtime'.containerd.runtimes.native]
  runtime_type = "io.containerd.runc.v2"
  snapshotter = "native"
`
	require.NoError(t, os.WriteFile(cfgPath, []byte(cfg), 0o600))

	ctrd := newCtrdProc(t, *containerdBin, workDir, nil)
	require.NoError(t, ctrd.isReady())

	iSvc := ctrd.criImageService(t)
	rSvc := ctrd.criRuntimeService(t)

	ctrdClient, err := containerd.New(ctrd.grpcAddress(), containerd.WithDefaultNamespace(k8sNamespace))
	require.NoError(t, err)

	t.Cleanup(func() {
		if t.Failed() {
			t.Log("Dumping containerd config and logs due to test failure")
			dumpFileContent(t, ctrd.configPath())
			dumpFileContent(t, ctrd.logPath())
		}
		assert.NoError(t, ctrdClient.Close())
		cleanupPods(t, rSvc)
		assert.NoError(t, ctrd.kill(syscall.SIGTERM))
		assert.NoError(t, ctrd.wait(5*time.Minute))
	})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// The native snapshotter must be registered and healthy, otherwise the
	// snapshotter-switch scenario cannot be exercised.
	resp, err := ctrdClient.IntrospectionService().Plugins(ctx, fmt.Sprintf("type==%s,id==%s", plugins.SnapshotPlugin, "native"))
	require.NoError(t, err)
	if len(resp.Plugins) == 0 {
		t.Skip("native snapshotter plugin is not registered")
	}
	if initErr := resp.Plugins[0].InitErr; initErr != nil {
		t.Skipf("native snapshotter plugin is not ready: %s", initErr.Message)
	}

	testImage := images.Get(images.BusyBox)

	// 1. Pull with the default handler. The image is unpacked into overlayfs
	//    and, because discard_unpacked_layers=true, its layer blobs become
	//    unreferenced in the content store.
	pullImagesByCRI(t, iSvc, testImage)

	img, err := ctrdClient.GetImage(context.Background(), testImage)
	require.NoError(t, err)

	diffIDs, err := img.RootFS(context.Background())
	require.NoError(t, err)
	chainIDs := identity.ChainIDs(diffIDs)

	manifest, err := containerdimages.Manifest(context.Background(), ctrdClient.ContentStore(), img.Target(), platforms.Default())
	require.NoError(t, err)
	require.NotEmpty(t, manifest.Layers)

	// 2. Drop the now-unreferenced layer blobs, emulating the garbage
	//    collection that discard_unpacked_layers relies on. content.Delete is
	//    a direct removal, so this is deterministic regardless of GC timing.
	cs := ctrdClient.ContentStore()
	layerDigests := make([]digest.Digest, 0, len(manifest.Layers))
	for _, layer := range manifest.Layers {
		layerDigests = append(layerDigests, layer.Digest)
		if err := cs.Delete(context.Background(), layer.Digest); err != nil && !errdefs.IsNotFound(err) {
			require.NoErrorf(t, err, "failed to delete layer blob %s", layer.Digest)
		}
	}
	for _, dgst := range layerDigests {
		_, err := cs.Info(context.Background(), dgst)
		require.Truef(t, errdefs.IsNotFound(err), "layer blob %s should be gone, got err=%v", dgst, err)
	}

	// Sanity: the image was never unpacked into the native snapshotter.
	nativeSn := ctrdClient.SnapshotService("native")
	for _, chainID := range chainIDs {
		_, err := nativeSn.Stat(context.Background(), chainID.String())
		require.Truef(t, errdefs.IsNotFound(err), "unexpected native snapshot for chainID %s", chainID)
	}

	// 3. The core assertion. ImageStatus for the native handler must report the
	//    image absent (no native snapshot, blobs gone — it cannot be used),
	//    while the default overlayfs handler still reports it present (the
	//    overlayfs snapshot is intact). Before the fix both reported present.
	nativeStatus, err := iSvc.ImageStatus(&criruntime.ImageSpec{Image: testImage, RuntimeHandler: "native"})
	require.NoError(t, err)
	assert.Nil(t, nativeStatus, "image unusable for the native snapshotter should be reported absent")

	overlayStatus, err := iSvc.ImageStatus(&criruntime.ImageSpec{Image: testImage, RuntimeHandler: ""})
	require.NoError(t, err)
	require.NotNil(t, overlayStatus, "image is still usable for the default overlayfs handler")

	// 4. Re-pull — the action kubelet takes in response to the absent status —
	//    restores the discarded layer blobs to the content store, healing the
	//    image. With a nil sandbox config the pull targets the default
	//    (overlayfs) snapshotter, whose chainID snapshots already exist; the
	//    unpacker therefore short-circuits the snapshot creation and the blobs
	//    are restored by its blob-refill preflight. Layer blobs are
	//    snapshotter-independent content, so this is what we assert on.
	_, err = iSvc.PullImage(&criruntime.ImageSpec{Image: testImage}, nil, nil, "")
	require.NoError(t, err)

	for _, dgst := range layerDigests {
		_, err := cs.Info(context.Background(), dgst)
		require.NoErrorf(t, err, "layer blob %s should be restored to the content store after re-pull", dgst)
	}
}

// TestImageStatusDiscardedLayersNoRuntimeHandler reproduces the same underlying
// problem in the configuration where kubelet's RuntimeClassInImageCriAPI
// feature gate is DISABLED, so ImageStatus is called with no runtime handler.
//
// This is the case the snapshotter-switch test cannot cover: with no handler,
// ImageStatus resolves the default snapshotter, whose chainID snapshot exists,
// so the snapshot-existence check passes. The only thing that can still detect
// the discarded content is the independent blob check (active because
// discard_unpacked_layers is false — the steady state after a node is switched
// off discard). It must report the image absent so kubelet re-pulls and
// restores the blobs; otherwise a later re-unpack (a different runtime's
// snapshotter, or any operation needing the layers) fails with the layer
// content missing from the content store.
func TestImageStatusDiscardedLayersNoRuntimeHandler(t *testing.T) {
	workDir := t.TempDir()
	cfgPath := filepath.Join(workDir, "config.toml")
	cfg := `
version = 3

[plugins.'io.containerd.cri.v1.images']
  snapshotter = "overlayfs"
  discard_unpacked_layers = false

[plugins.'io.containerd.cri.v1.runtime'.containerd]
  default_runtime_name = "runc"

[plugins.'io.containerd.cri.v1.runtime'.containerd.runtimes.runc]
  runtime_type = "io.containerd.runc.v2"
  snapshotter = "overlayfs"
`
	require.NoError(t, os.WriteFile(cfgPath, []byte(cfg), 0o600))

	ctrd := newCtrdProc(t, *containerdBin, workDir, nil)
	require.NoError(t, ctrd.isReady())

	iSvc := ctrd.criImageService(t)
	rSvc := ctrd.criRuntimeService(t)

	ctrdClient, err := containerd.New(ctrd.grpcAddress(), containerd.WithDefaultNamespace(k8sNamespace))
	require.NoError(t, err)

	t.Cleanup(func() {
		if t.Failed() {
			t.Log("Dumping containerd config and logs due to test failure")
			dumpFileContent(t, ctrd.configPath())
			dumpFileContent(t, ctrd.logPath())
		}
		assert.NoError(t, ctrdClient.Close())
		cleanupPods(t, rSvc)
		assert.NoError(t, ctrd.kill(syscall.SIGTERM))
		assert.NoError(t, ctrd.wait(5*time.Minute))
	})

	testImage := images.Get(images.BusyBox)

	// 1. Pull (no handler) into the default overlayfs snapshotter. With
	//    discard_unpacked_layers=false the layer blobs are retained.
	pullImagesByCRI(t, iSvc, testImage)

	img, err := ctrdClient.GetImage(context.Background(), testImage)
	require.NoError(t, err)
	manifest, err := containerdimages.Manifest(context.Background(), ctrdClient.ContentStore(), img.Target(), platforms.Default())
	require.NoError(t, err)
	require.NotEmpty(t, manifest.Layers)

	// 2. Emulate the corrupt state a prior discard_unpacked_layers=true run
	//    leaves once the node is switched to discard=false: the overlayfs
	//    snapshot is intact, but the layer blobs are gone from the content
	//    store.
	cs := ctrdClient.ContentStore()
	layerDigests := make([]digest.Digest, 0, len(manifest.Layers))
	for _, layer := range manifest.Layers {
		layerDigests = append(layerDigests, layer.Digest)
		if err := cs.Delete(context.Background(), layer.Digest); err != nil && !errdefs.IsNotFound(err) {
			require.NoErrorf(t, err, "failed to delete layer blob %s", layer.Digest)
		}
	}
	for _, dgst := range layerDigests {
		_, err := cs.Info(context.Background(), dgst)
		require.Truef(t, errdefs.IsNotFound(err), "layer blob %s should be gone, got err=%v", dgst, err)
	}

	// 3. The core assertion. ImageStatus with NO runtime handler (gate
	//    disabled) must still report the image absent: the snapshot exists but
	//    the blobs are gone, and discard_unpacked_layers=false means they are
	//    expected to be present. Reporting present here would leave the image
	//    un-reunpackable with no re-pull triggered.
	status, err := iSvc.ImageStatus(&criruntime.ImageSpec{Image: testImage})
	require.NoError(t, err)
	assert.Nil(t, status, "image with discarded blobs (discard=false, no handler) should be reported absent")

	// 4. Re-pull heals it: the blobs are restored to the content store.
	_, err = iSvc.PullImage(&criruntime.ImageSpec{Image: testImage}, nil, nil, "")
	require.NoError(t, err)

	for _, dgst := range layerDigests {
		_, err := cs.Info(context.Background(), dgst)
		require.NoErrorf(t, err, "layer blob %s should be restored to the content store after re-pull", dgst)
	}

	status, err = iSvc.ImageStatus(&criruntime.ImageSpec{Image: testImage})
	require.NoError(t, err)
	require.NotNil(t, status, "image should be reported present again after re-pull")
}
