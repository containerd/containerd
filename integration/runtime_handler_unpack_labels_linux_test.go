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
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/containerd/errdefs"
	"github.com/containerd/platforms"
	"github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/identity"
	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	criruntime "k8s.io/cri-api/pkg/apis/runtime/v1"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/content"
	coreimages "github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/integration/images"
	"github.com/containerd/containerd/v2/internal/cri/labels"
	imagestore "github.com/containerd/containerd/v2/internal/cri/store/image"
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

	img, err := ctrdClient.GetImage(context.Background(), imagestore.Name(nginxImage, "runc"))
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

	nginxImage = "quay.io/containerd/nginx:latest"
	metadata := img.Metadata()
	metadata.Name = imagestore.Name(nginxImage, "erofs")
	_, err = ctrdClient.ImageService().Create(context.Background(), metadata)
	require.NoError(t, err)
	require.NoError(t, Eventually(func() (bool, error) {
		image, err := iSvc.ImageStatus(&criruntime.ImageSpec{Image: nginxImage, RuntimeHandler: "erofs"})
		return image != nil, err
	}, 100*time.Millisecond, 10*time.Second))

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
		assert.Equal(t, nginxImage, snInfo.Labels[snpkg.TargetRefLabel])
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

func TestImagePlatformConsumption(t *testing.T) {
	workDir := t.TempDir()
	pauseRef := images.Get(images.Pause)
	cfg := fmt.Sprintf(`version = 3
[plugins.'io.containerd.cri.v1.images']
  snapshotter = "native"
[plugins.'io.containerd.cri.v1.images'.pinned_images]
  sandbox = %q
[plugins.'io.containerd.cri.v1.runtime'.containerd.runtimes.runc]
  runtime_type = "io.containerd.runc.v2"
  snapshotter = "native"
`, pauseRef)
	require.NoError(t, os.WriteFile(filepath.Join(workDir, "config.toml"), []byte(cfg), 0o600))
	ctrd := newCtrdProc(t, *containerdBin, workDir, nil)
	t.Cleanup(func() {
		assert.NoError(t, ctrd.kill(syscall.SIGTERM))
		assert.NoError(t, ctrd.wait(time.Minute))
		if t.Failed() {
			dumpFileContent(t, ctrd.logPath())
		}
	})
	require.NoError(t, ctrd.isReady())
	rSvc := ctrd.criRuntimeService(t)
	iSvc := ctrd.criImageService(t)
	client, err := containerd.New(ctrd.grpcAddress(), containerd.WithDefaultNamespace(k8sNamespace))
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, client.Close()) })
	t.Cleanup(func() { cleanupPods(t, rSvc) })
	ctx, done, err := client.WithLease(context.Background())
	require.NoError(t, err)
	defer done(ctx)
	cs := client.ContentStore()
	writeJSON := func(mediaType string, value any) imagespec.Descriptor {
		data, err := json.Marshal(value)
		require.NoError(t, err)
		desc := imagespec.Descriptor{MediaType: mediaType, Digest: digest.FromBytes(data), Size: int64(len(data))}
		require.NoError(t, content.WriteBlob(ctx, cs, desc.Digest.String(), bytes.NewReader(data), desc))
		return desc
	}
	selectedPlatform := platforms.DefaultSpec()
	selectedPlatform.Architecture = "arm"
	selectedPlatform.Variant = "v6"
	if platforms.DefaultSpec().Architecture == "arm" {
		selectedPlatform.Architecture = "amd64"
		selectedPlatform.Variant = ""
	}
	for _, source := range []string{images.Get(images.BusyBox), pauseRef} {
		t.Run(source, func(t *testing.T) {
			require.NoError(t, iSvc.RemoveImage(&criruntime.ImageSpec{Image: source}))
			base, err := client.Pull(ctx, source)
			require.NoError(t, err)
			manifest, err := coreimages.Manifest(ctx, cs, base.Target(), platforms.Default())
			require.NoError(t, err)
			baseSpec, err := base.Spec(ctx)
			require.NoError(t, err)
			var index imagespec.Index
			var selectedID digest.Digest
			var selectedChain string
			for _, platform := range []imagespec.Platform{platforms.DefaultSpec(), selectedPlatform} {
				var layer bytes.Buffer
				tw := tar.NewWriter(&layer)
				marker := platforms.Format(platform)
				require.NoError(t, tw.WriteHeader(&tar.Header{Name: "pr1-platform", Mode: 0o644, Size: int64(len(marker))}))
				_, err := tw.Write([]byte(marker))
				require.NoError(t, err)
				require.NoError(t, tw.Close())
				layerDesc := imagespec.Descriptor{MediaType: imagespec.MediaTypeImageLayer, Digest: digest.FromBytes(layer.Bytes()), Size: int64(layer.Len())}
				require.NoError(t, content.WriteBlob(ctx, cs, layerDesc.Digest.String(), bytes.NewReader(layer.Bytes()), layerDesc))
				spec := baseSpec
				spec.Platform = platform
				spec.Variant = "" // Older image configs may omit the manifest platform variant.
				spec.RootFS.DiffIDs = append(append([]digest.Digest{}, baseSpec.RootFS.DiffIDs...), layerDesc.Digest)
				m := manifest
				m.Config = writeJSON(imagespec.MediaTypeImageConfig, spec)
				m.Layers = append(append([]imagespec.Descriptor{}, manifest.Layers...), layerDesc)
				desc := writeJSON(imagespec.MediaTypeImageManifest, m)
				desc.Platform = &platform
				index.Manifests = append(index.Manifests, desc)
				if platform.Architecture == selectedPlatform.Architecture {
					selectedID = m.Config.Digest
					selectedChain = identity.ChainID(spec.RootFS.DiffIDs).String()
				}
			}
			index.MediaType = imagespec.MediaTypeImageIndex
			index.SchemaVersion = 2
			img := coreimages.Image{Name: source, Target: writeJSON(imagespec.MediaTypeImageIndex, index), Labels: map[string]string{labels.ImagePlatformLabelKey: platforms.Format(selectedPlatform)}}
			_, err = client.ImageService().Update(ctx, img, "target", "labels."+labels.ImagePlatformLabelKey)
			require.NoError(t, err)
			require.NoError(t, Eventually(func() (bool, error) {
				image, err := iSvc.ImageStatus(&criruntime.ImageSpec{Image: source})
				return image != nil && image.Id == selectedID.String(), err
			}, 100*time.Millisecond, 10*time.Second))
			_, err = client.SnapshotService("native").Stat(ctx, selectedChain)
			require.True(t, errdefs.IsNotFound(err), "selected image must exercise unpack fallback")
			sbConfig := PodSandboxConfig("platform", "image-platform", WithHostNetwork)
			sb, err := rSvc.RunPodSandbox(sbConfig, "")
			require.NoError(t, err)
			if source == pauseRef {
				container, err := client.LoadContainer(ctx, sb)
				require.NoError(t, err)
				info, err := container.Info(ctx)
				require.NoError(t, err)
				snapshot, err := client.SnapshotService("native").Stat(ctx, info.SnapshotKey)
				require.NoError(t, err)
				assert.Equal(t, selectedChain, snapshot.Parent)
			} else {
				t.Run("two-platform-volumes", func(t *testing.T) {
					hostRef := source + "-host-platform"
					hostConfig, err := coreimages.Config(ctx, cs, img.Target, platforms.Default())
					require.NoError(t, err)
					_, err = client.ImageService().Create(ctx, coreimages.Image{Name: hostRef, Target: img.Target,
						Labels: map[string]string{labels.ImagePlatformLabelKey: platforms.Format(platforms.DefaultSpec())}})
					require.NoError(t, err)
					require.NoError(t, Eventually(func() (bool, error) {
						image, err := iSvc.ImageStatus(&criruntime.ImageSpec{Image: hostRef})
						return image != nil && image.Id == hostConfig.Digest.String(), err
					}, 100*time.Millisecond, 10*time.Second))
					config := ContainerConfig("two-platforms", source, WithCommand("sleep", "300"),
						WithImageVolumeMount(source, "", "/selected-image"), WithImageVolumeMount(hostRef, "", "/host-image"))
					cn, err := rSvc.CreateContainer(sb, config, sbConfig)
					require.NoError(t, err)
					require.NoError(t, rSvc.StartContainer(cn))
					for path, expected := range map[string]string{
						"/pr1-platform":                platforms.Format(selectedPlatform),
						"/selected-image/pr1-platform": platforms.Format(selectedPlatform),
						"/host-image/pr1-platform":     platforms.Format(platforms.DefaultSpec()),
					} {
						stdout, stderr, err := rSvc.ExecSync(cn, []string{"cat", path}, time.Minute)
						require.NoError(t, err, string(stderr))
						assert.Equal(t, expected, string(stdout))
					}
				})
			}
			require.NoError(t, rSvc.StopPodSandbox(sb))
			require.NoError(t, rSvc.RemovePodSandbox(sb))
		})
	}
}

func TestRuntimeHandlerPullIsolation(t *testing.T) {
	for _, local := range []bool{true, false} {
		t.Run(fmt.Sprintf("local=%t", local), func(t *testing.T) {
			entered, release := make(chan struct{}, 1), make(chan struct{})
			registry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				select {
				case entered <- struct{}{}:
				default:
				}
				select {
				case <-release:
					w.WriteHeader(http.StatusServiceUnavailable)
				case <-r.Context().Done():
				}
			}))
			defer registry.Close()
			defer close(release)
			host := strings.TrimPrefix(registry.URL, "http://")
			workDir := t.TempDir()
			cfgPath := filepath.Join(workDir, "config.toml")
			hostsDir := filepath.Join(workDir, "hosts")
			require.NoError(t, os.MkdirAll(filepath.Join(hostsDir, host), 0700))
			require.NoError(t, os.WriteFile(filepath.Join(hostsDir, host, "hosts.toml"), []byte(fmt.Sprintf("server = %q\n", registry.URL)), 0600))
			require.NoError(t, os.WriteFile(cfgPath, []byte(fmt.Sprintf(`
version = 3
[plugins.'io.containerd.cri.v1.images']
  use_local_image_pull = %t
[plugins.'io.containerd.cri.v1.images'.registry]
  config_path = %q
[plugins.'io.containerd.cri.v1.runtime'.containerd.runtimes.a]
  runtime_type = "io.containerd.runc.v2"
[plugins.'io.containerd.cri.v1.runtime'.containerd.runtimes.b]
  runtime_type = "io.containerd.runc.v2"
`, local, hostsDir)), 0600))
			proc := newCtrdProc(t, *containerdBin, workDir, []string{"--config", cfgPath})
			require.NoError(t, proc.isReady())
			t.Cleanup(func() {
				assert.NoError(t, proc.kill(syscall.SIGTERM))
				assert.NoError(t, proc.wait(time.Minute))
				if t.Failed() {
					dumpFileContent(t, proc.logPath())
				}
			})
			conn, err := grpc.NewClient("unix://"+proc.grpcAddress(), grpc.WithTransportCredentials(insecure.NewCredentials()))
			require.NoError(t, err)
			defer conn.Close()
			service := criruntime.NewImageServiceClient(conn)
			ctx := context.Background()
			ref := images.Get(images.BusyBox)
			for _, handler := range []string{"a", "b"} {
				_, err := service.PullImage(ctx, &criruntime.PullImageRequest{
					Image: &criruntime.ImageSpec{Image: ref, RuntimeHandler: handler},
				})
				require.NoError(t, err)
			}
			slowCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			finished := make(chan error, 1)
			go func() {
				_, err := service.PullImage(slowCtx, &criruntime.PullImageRequest{
					Image: &criruntime.ImageSpec{Image: host + "/stalled:latest", RuntimeHandler: "a"},
				})
				finished <- err
			}()
			select {
			case <-entered:
			case err := <-finished:
				t.Fatalf("pull failed before contacting the registry: %v", err)
			case <-time.After(30 * time.Second):
				t.Fatal("pull did not contact the registry")
			}
			t.Run("same-handler-pull", func(t *testing.T) {
				ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
				defer cancel()
				_, err := service.PullImage(ctx, &criruntime.PullImageRequest{
					Image: &criruntime.ImageSpec{Image: ref, RuntimeHandler: "a"},
				})
				require.NoError(t, err)
			})
			t.Run("other-handler-remove", func(t *testing.T) {
				ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
				defer cancel()
				_, err := service.RemoveImage(ctx, &criruntime.RemoveImageRequest{
					Image: &criruntime.ImageSpec{Image: ref, RuntimeHandler: "b"},
				})
				require.NoError(t, err)
				status, err := service.ImageStatus(ctx, &criruntime.ImageStatusRequest{
					Image: &criruntime.ImageSpec{Image: ref, RuntimeHandler: "a"},
				})
				require.NoError(t, err)
				require.NotNil(t, status.Image)
			})
			cancel()
			select {
			case err := <-finished:
				require.Error(t, err)
			case <-time.After(5 * time.Second):
				t.Fatal("canceled registry pull did not return")
			}
			client, err := containerd.New(proc.grpcAddress(), containerd.WithDefaultNamespace(k8sNamespace))
			require.NoError(t, err)
			defer client.Close()
			require.NoError(t, Eventually(func() (bool, error) {
				leases, err := client.LeasesService().List(ctx)
				return len(leases) == 0, err
			}, 100*time.Millisecond, 10*time.Second))
		})
	}
}

func TestRuntimeHandlerImageLifecycle(t *testing.T) {
	pauseRef := images.Get(images.Pause)
	_, err := imageService.PullImage(&criruntime.ImageSpec{Image: pauseRef}, nil, nil, "")
	require.NoError(t, err)
	pause, err := imageService.ImageStatus(&criruntime.ImageSpec{Image: pauseRef})
	require.NoError(t, err)
	require.NotNil(t, pause)
	require.NotEmpty(t, pause.RepoDigests)
	_, pauseDigest, ok := strings.Cut(pause.RepoDigests[0], "@")
	require.True(t, ok)
	pauseRef += "@" + pauseDigest
	for _, test := range []struct {
		local       bool
		snapshotter string
	}{
		{local: true, snapshotter: "overlayfs"},
		{snapshotter: "native"},
	} {
		t.Run(fmt.Sprintf("local=%t/b=%s", test.local, test.snapshotter), func(t *testing.T) {
			workDir := t.TempDir()
			cfg := fmt.Sprintf(`version = 3
[plugins.'io.containerd.cri.v1.images']
  use_local_image_pull = %t
[plugins.'io.containerd.cri.v1.images'.pinned_images]
  sandbox = %q
[plugins.'io.containerd.cri.v1.runtime'.containerd.runtimes.a]
  runtime_type = "io.containerd.runc.v2"
  snapshotter = "overlayfs"
[plugins.'io.containerd.cri.v1.runtime'.containerd.runtimes.b]
  runtime_type = "io.containerd.runc.v2"
  snapshotter = %q
`, test.local, pauseRef, test.snapshotter)
			require.NoError(t, os.WriteFile(filepath.Join(workDir, "config.toml"), []byte(cfg), 0o600))
			ctrd := newCtrdProc(t, *containerdBin, workDir, nil)
			require.NoError(t, ctrd.isReady())
			rSvc := ctrd.criRuntimeService(t)
			iSvc := ctrd.criImageService(t)
			client, err := containerd.New(ctrd.grpcAddress(), containerd.WithDefaultNamespace(k8sNamespace))
			require.NoError(t, err)
			t.Cleanup(func() {
				cleanupPods(t, rSvc)
				assert.NoError(t, client.Close())
				assert.NoError(t, ctrd.kill(syscall.SIGTERM))
				assert.NoError(t, ctrd.wait(time.Minute))
				if t.Failed() {
					dumpFileContent(t, ctrd.logPath())
				}
			})
			ctx := context.Background()
			ref := images.Get(images.BusyBox)
			legacy, err := client.Pull(ctx, ref)
			require.NoError(t, err)

			config, err := legacy.Config(ctx)
			require.NoError(t, err)
			id := config.Digest.String()
			diffIDs, err := legacy.RootFS(ctx)
			require.NoError(t, err)
			chainID := identity.ChainID(diffIDs).String()
			require.NoError(t, Eventually(func() (bool, error) {
				image, err := iSvc.ImageStatus(&criruntime.ImageSpec{Image: ref})
				return image != nil && image.Spec.GetRuntimeHandler() == "runc", err
			}, 100*time.Millisecond, 10*time.Second))
			missing, err := iSvc.ImageStatus(&criruntime.ImageSpec{Image: id, RuntimeHandler: "a"})
			require.NoError(t, err)
			require.Nil(t, missing)
			_, err = iSvc.PullImage(&criruntime.ImageSpec{Image: ref, RuntimeHandler: "unknown"}, nil, nil, "unknown")
			require.Error(t, err)

			pods := make(map[string]*criruntime.PodSandboxConfig)
			sandboxes := make(map[string]string)
			containers := make(map[string]string)
			for _, handler := range []string{"a", "b"} {
				pods[handler] = PodSandboxConfig(handler, "handler-images", WithHostNetwork)
				sandboxes[handler], err = rSvc.RunPodSandbox(pods[handler], handler)
				require.NoError(t, err)
			}
			legacyConfig := ContainerConfig("legacy", id, WithCommand("sleep", "300"), WithImageVolumeMount(id, "", "/image-volume"))
			legacyConfig.Image.RuntimeHandler = "a"
			_, err = rSvc.CreateContainer(sandboxes["a"], legacyConfig, pods["a"])
			require.Error(t, err)
			legacyConfig.Image.RuntimeHandler = ""
			legacyConfig.Mounts[0].Image.RuntimeHandler = "a"
			_, err = rSvc.CreateContainer(sandboxes["a"], legacyConfig, pods["a"])
			require.Error(t, err)
			legacyConfig.Mounts[0].Image.RuntimeHandler = ""
			legacyContainer, err := rSvc.CreateContainer(sandboxes["a"], legacyConfig, pods["a"])
			require.NoError(t, err)
			require.NoError(t, rSvc.StartContainer(legacyContainer))
			status, err := rSvc.ContainerStatus(legacyContainer)
			require.NoError(t, err)
			require.Equal(t, "runc", status.Image.GetRuntimeHandler())
			stdout, stderr, err := rSvc.ExecSync(legacyContainer, []string{"test", "-x", "/image-volume/bin/busybox"}, time.Minute)
			require.NoError(t, err, string(stdout), string(stderr))
			create := func(handler, name string) string {
				cfg := ContainerConfig(name, id, WithCommand("sleep", "300"), WithImageVolumeMount(id, "", "/image-volume"))
				cfg.Image.RuntimeHandler = handler
				cfg.Mounts[0].Image.RuntimeHandler = handler
				cn, err := rSvc.CreateContainer(sandboxes[handler], cfg, pods[handler])
				require.NoError(t, err)
				require.NoError(t, rSvc.StartContainer(cn))
				status, err := rSvc.ContainerStatus(cn)
				require.NoError(t, err)
				require.Len(t, status.Mounts, 1)
				require.Equal(t, handler, status.Mounts[0].Image.GetRuntimeHandler())
				return cn
			}
			for _, handler := range []string{"a", "b"} {
				pulled, err := iSvc.PullImage(&criruntime.ImageSpec{Image: ref}, nil, nil, handler)
				require.NoError(t, err)
				require.Equal(t, id, pulled)
				containers[handler] = create(handler, "before-restart")
			}
			listed, err := iSvc.ListImages(nil)
			require.NoError(t, err)
			handlers := make(map[string]bool)
			for _, image := range listed {
				if image.Id == id {
					handlers[image.Spec.GetRuntimeHandler()] = true
					require.Contains(t, image.RepoTags, ref)
				}
			}
			require.Equal(t, map[string]bool{"runc": true, "a": true, "b": true}, handlers)
			filtered, err := iSvc.ListImages(&criruntime.ImageFilter{Image: &criruntime.ImageSpec{Image: id, RuntimeHandler: "b"}})
			require.NoError(t, err)
			require.Len(t, filtered, 1)
			require.Equal(t, "b", filtered[0].Spec.GetRuntimeHandler())

			require.NoError(t, ctrd.kill(syscall.SIGTERM))
			require.NoError(t, ctrd.wait(time.Minute))
			ctrd = newCtrdProc(t, *containerdBin, workDir, nil)
			require.NoError(t, ctrd.isReady())
			for _, handler := range []string{"a", "b"} {
				image, err := iSvc.ImageStatus(&criruntime.ImageSpec{Image: id, RuntimeHandler: handler})
				require.NoError(t, err)
				require.NotNil(t, image)
				status, err := rSvc.ContainerStatus(containers[handler])
				require.NoError(t, err)
				require.Equal(t, id, status.ImageId)
				require.Equal(t, handler, status.Image.GetRuntimeHandler())
				stdout, _, err := rSvc.ExecSync(containers[handler], []string{"echo", "recovered"}, time.Minute)
				require.NoError(t, err)
				require.Equal(t, "recovered\n", string(stdout))
			}
			require.NoError(t, iSvc.RemoveImage(&criruntime.ImageSpec{Image: id}))
			image, err := iSvc.ImageStatus(&criruntime.ImageSpec{Image: id, RuntimeHandler: "runc"})
			require.NoError(t, err)
			require.Nil(t, image)
			require.NoError(t, iSvc.RemoveImage(&criruntime.ImageSpec{Image: id, RuntimeHandler: "a"}))
			require.NoError(t, iSvc.RemoveImage(&criruntime.ImageSpec{Image: id, RuntimeHandler: "a"}))
			image, err = iSvc.ImageStatus(&criruntime.ImageSpec{Image: id, RuntimeHandler: "b"})
			require.NoError(t, err)
			require.NotNil(t, image)
			create("b", "after-delete-without-pull")
			cleanupPods(t, rSvc)
			cfg, _, _ = strings.Cut(cfg, "[plugins.'io.containerd.cri.v1.runtime'.containerd.runtimes.b]")
			require.NoError(t, os.WriteFile(filepath.Join(workDir, "config.toml"), []byte(cfg), 0o600))
			require.NoError(t, ctrd.kill(syscall.SIGTERM))
			require.NoError(t, ctrd.wait(time.Minute))
			ctrd = newCtrdProc(t, *containerdBin, workDir, nil)
			require.NoError(t, ctrd.isReady())
			image, err = iSvc.ImageStatus(&criruntime.ImageSpec{Image: id, RuntimeHandler: "b"})
			require.NoError(t, err)
			require.NotNil(t, image)
			_, err = iSvc.PullImage(&criruntime.ImageSpec{Image: ref, RuntimeHandler: "b"}, nil, nil, "b")
			require.Error(t, err)
			require.NoError(t, iSvc.RemoveImage(&criruntime.ImageSpec{Image: id, RuntimeHandler: "b"}))
			require.NoError(t, Eventually(func() (bool, error) {
				_, err := client.ContentStore().Info(ctx, config.Digest)
				return errdefs.IsNotFound(err), nil
			}, 100*time.Millisecond, 10*time.Second))
			for _, sn := range []string{"overlayfs", test.snapshotter} {
				require.NoError(t, Eventually(func() (bool, error) {
					_, err := client.SnapshotService(sn).Stat(ctx, chainID)
					return errdefs.IsNotFound(err), nil
				}, 100*time.Millisecond, 10*time.Second))
			}
		})
	}
}
