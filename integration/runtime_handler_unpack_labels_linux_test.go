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
	"github.com/opencontainers/image-spec/identity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	criruntime "k8s.io/cri-api/pkg/apis/runtime/v1"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/integration/images"
	snpkg "github.com/containerd/containerd/v2/pkg/snapshotters"
	"github.com/containerd/containerd/v2/plugins"
)

func TestRuntimeHandlerUnpackWithSnapshotLabels(t *testing.T) {
	workDir := t.TempDir()
	cfgPath := filepath.Join(workDir, "config.toml")
	cfg := `
version = 3

[plugins.'io.containerd.cri.v1.images']
  snapshotter = "overlayfs"
  disable_snapshot_annotations = false

[plugins.'io.containerd.cri.v1.runtime'.containerd]
  default_runtime_name = "runc"

[plugins.'io.containerd.cri.v1.runtime'.containerd.runtimes.runc]
  runtime_type = "io.containerd.runc.v2"
  snapshotter = "overlayfs"

[plugins.'io.containerd.cri.v1.runtime'.containerd.runtimes.erofs]
  runtime_type = "io.containerd.runc.v2"
  snapshotter = "erofs"
`
	require.NoError(t, os.WriteFile(cfgPath, []byte(cfg), 0o600))

	ctrd := newCtrdProc(t, *containerdBin, workDir, nil)
	require.NoError(t, ctrd.isReady())

	rSvc := ctrd.criRuntimeService(t)
	iSvc := ctrd.criImageService(t)

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
	resp, err := ctrdClient.IntrospectionService().Plugins(ctx, fmt.Sprintf("type==%s,id==%s", plugins.SnapshotPlugin, "erofs"))
	require.NoError(t, err)
	if len(resp.Plugins) == 0 {
		t.Skip("erofs snapshotter plugin is not registered")
	}
	if initErr := resp.Plugins[0].InitErr; initErr != nil {
		t.Skipf("erofs snapshotter plugin is not ready: %s", initErr.Message)
	}

	nginxImage := images.Get(images.Nginx)
	pullImagesByCRI(t, iSvc, nginxImage)

	img, err := ctrdClient.GetImage(context.Background(), nginxImage)
	require.NoError(t, err)
	diffIDs, err := img.RootFS(context.Background())
	require.NoError(t, err)
	chainIDs := identity.ChainIDs(diffIDs)

	// First pod uses default runtime handler (overlayfs). No containers created.
	sb1Cfg := PodSandboxConfig("overlay-pod", "runtime-handler-unpack")
	_, err = rSvc.RunPodSandbox(sb1Cfg, "")
	require.NoError(t, err)

	// Image is pulled with overlayfs; nginx snapshots should not exist on erofs yet.
	erofsSn := ctrdClient.SnapshotService("erofs")
	for _, chainID := range chainIDs {
		_, err := erofsSn.Stat(context.Background(), chainID.String())
		assert.Truef(t, errdefs.IsNotFound(err), "expected no erofs snapshot for chainID %s before erofs container creation, got err=%v", chainID, err)
	}

	// Second pod uses erofs runtime handler. Creating nginx container should trigger
	// automatic unpack for erofs with snapshot labels.
	sb2Cfg := PodSandboxConfig("erofs-pod", "runtime-handler-unpack")
	sb2ID, err := rSvc.RunPodSandbox(sb2Cfg, "erofs")
	require.NoError(t, err)
	cn2Cfg := ContainerConfig("erofs-container", nginxImage, WithCommand("sleep", "1d"))
	cn2ID, err := rSvc.CreateContainer(sb2ID, cn2Cfg, sb2Cfg)
	require.NoError(t, err)

	for _, chainID := range chainIDs {
		snInfo, err := erofsSn.Stat(context.Background(), chainID.String())
		require.NoErrorf(t, err, "failed to stat erofs snapshot for chainID %s", chainID)
		require.NotNil(t, snInfo.Labels)
		assert.NotEmpty(t, snInfo.Labels[snpkg.TargetRefLabel], "missing %s on chainID %s", snpkg.TargetRefLabel, chainID)
		assert.NotEmpty(t, snInfo.Labels[snpkg.TargetManifestDigestLabel], "missing %s on chainID %s", snpkg.TargetManifestDigestLabel, chainID)
		assert.NotEmpty(t, snInfo.Labels[snpkg.TargetLayerDigestLabel], "missing %s on chainID %s", snpkg.TargetLayerDigestLabel, chainID)
		assert.NotEmpty(t, snInfo.Labels[snpkg.TargetImageLayersLabel], "missing %s on chainID %s", snpkg.TargetImageLayersLabel, chainID)
	}

	// Make sure the second pod really used the erofs runtime handler path.
	sb2Status, err := rSvc.PodSandboxStatus(sb2ID)
	require.NoError(t, err)
	assert.Equal(t, "erofs", sb2Status.RuntimeHandler)

	// Container should be created.
	cn2Status, err := rSvc.ContainerStatus(cn2ID)
	require.NoError(t, err)
	assert.Equal(t, criruntime.ContainerState_CONTAINER_CREATED, cn2Status.State)
}
