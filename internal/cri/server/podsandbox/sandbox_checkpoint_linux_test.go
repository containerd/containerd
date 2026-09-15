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

package podsandbox

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/containers"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/core/sandbox"
	crilabels "github.com/containerd/containerd/v2/internal/cri/labels"
	containerstore "github.com/containerd/containerd/v2/internal/cri/store/container"
	"github.com/containerd/errdefs"
	"github.com/containerd/typeurl/v2"
	"github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/specs-go"
	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

func TestNewCheckpointServiceOwnsItsState(t *testing.T) {
	_, err := NewCheckpointService(CheckpointServiceOptions{RootDir: t.TempDir()})
	require.ErrorContains(t, err, "containerd client")

	_, err = NewCheckpointService(CheckpointServiceOptions{Client: new(client.Client)})
	require.ErrorContains(t, err, "root directory")

	root := filepath.Join(t.TempDir(), "controller-checkpoints")
	service, err := NewCheckpointService(CheckpointServiceOptions{
		Client:  new(client.Client),
		RootDir: root,
	})
	require.NoError(t, err)
	assert.Equal(t, root, service.rootDir)
	info, err := os.Stat(root)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), info.Mode().Perm())

	symlink := filepath.Join(t.TempDir(), "controller-checkpoints")
	require.NoError(t, os.Symlink(root, symlink))
	_, err = NewCheckpointService(CheckpointServiceOptions{
		Client:  new(client.Client),
		RootDir: symlink,
	})
	require.ErrorContains(t, err, "not a real directory")
}

func TestCheckpointOutputReservation(t *testing.T) {
	service := new(CheckpointService)
	output := t.TempDir()

	release, err := service.reservePodCheckpointOutput(output)
	require.NoError(t, err)
	_, err = service.reservePodCheckpointOutput(output)
	require.ErrorContains(t, err, "already in use")
	release()

	release, err = service.reservePodCheckpointOutput(output)
	require.NoError(t, err)
	release()

	require.NoError(t, os.WriteFile(filepath.Join(output, "existing"), []byte("data"), 0o600))
	_, err = service.reservePodCheckpointOutput(output)
	require.ErrorContains(t, err, "must be empty")
}

func TestValidateCheckpointOutputPathRejectsUnsafePaths(t *testing.T) {
	require.Error(t, validateCheckpointOutputPath("relative"))

	parent := t.TempDir()
	realDir := filepath.Join(parent, "real")
	require.NoError(t, os.Mkdir(realDir, 0o700))
	symlink := filepath.Join(parent, "link")
	require.NoError(t, os.Symlink(realDir, symlink))
	require.ErrorContains(t, validateCheckpointOutputPath(symlink), "not a real directory")

	file := filepath.Join(parent, "file")
	require.NoError(t, os.WriteFile(file, nil, 0o600))
	require.ErrorContains(t, validateCheckpointOutputPath(file), "not a real directory")
}

func TestPrepareRestoreContainersUsesOnlyOptionDataAndCheckpointFiles(t *testing.T) {
	options, checkpointConfig, restoreConfig := writeRestoreFixture(t)

	containers, err := prepareRestoreContainers(options)
	require.NoError(t, err)
	defer closeRestoreContainers(containers)
	require.Len(t, containers, 1)
	assert.Equal(t, "new-container-id", containers[0].id)
	assert.Equal(t, "app", containers[0].name)
	assert.Equal(t, filepath.Join(options.CheckpointPath, checkpointArchiveName("old-container-id")), containers[0].archive.Name())
	assert.Equal(t, digest.FromString("base-image"), containers[0].imageRef)

	t.Run("container config mismatch", func(t *testing.T) {
		bad := proto.Clone(restoreConfig).(*runtime.ContainerConfig)
		bad.Command = []string{"different"}
		badAny, err := typeurl.MarshalAny(bad)
		require.NoError(t, err)
		mismatch := options
		mismatch.Containers = append([]sandbox.RestoreContainer(nil), options.Containers...)
		mismatch.Containers[0].Config = badAny
		_, err = prepareRestoreContainers(mismatch)
		require.ErrorIs(t, err, errdefs.ErrFailedPrecondition)
	})

	t.Run("sandbox config mismatch", func(t *testing.T) {
		bad := proto.Clone(checkpointConfig).(*runtime.PodSandboxConfig)
		bad.Hostname = "different"
		badAny, err := typeurl.MarshalAny(bad)
		require.NoError(t, err)
		mismatch := options
		mismatch.SandboxConfig = badAny
		_, err = prepareRestoreContainers(mismatch)
		require.ErrorIs(t, err, errdefs.ErrFailedPrecondition)
	})

	t.Run("container identity mismatch", func(t *testing.T) {
		bad := proto.Clone(restoreConfig).(*runtime.ContainerConfig)
		bad.Metadata.Name = "different"
		badAny, err := typeurl.MarshalAny(bad)
		require.NoError(t, err)
		mismatch := options
		mismatch.Containers = append([]sandbox.RestoreContainer(nil), options.Containers...)
		mismatch.Containers[0].Config = badAny
		_, err = prepareRestoreContainers(mismatch)
		require.ErrorContains(t, err, "does not match option name")
	})

	t.Run("controller validates its own archive layout", func(t *testing.T) {
		manifest, err := readPodCheckpointManifest(options.CheckpointPath)
		require.NoError(t, err)
		manifest.Containers[0].Archive = "../outside"
		writeJSONFile(t, filepath.Join(options.CheckpointPath, podCheckpointManifestFile), manifest)
		_, err = prepareRestoreContainers(options)
		require.ErrorContains(t, err, "invalid archive name")
	})
}

func TestPodCheckpointOptionsAreControllerSpecific(t *testing.T) {
	require.NoError(t, validatePodCheckpointOptions(nil))
	require.NoError(t, validatePodRestoreOptions(nil))

	err := validatePodCheckpointOptions(map[string]string{"future-controller-option": "value"})
	require.ErrorIs(t, err, errdefs.ErrInvalidArgument)
	require.ErrorContains(t, err, "pause controller")
}

func TestDecodePodCheckpointManifestIsStrict(t *testing.T) {
	valid, err := json.Marshal(podCheckpointManifest{
		Version:   podCheckpointManifestVersion,
		SandboxID: "sandbox",
		Containers: []podCheckpointManifestContainer{{
			Name:    "app",
			ID:      "container",
			Archive: checkpointArchiveName("container"),
			Config:  json.RawMessage(`{}`),
			Status:  json.RawMessage(`{}`),
		}},
	})
	require.NoError(t, err)

	_, err = decodePodCheckpointManifest(append(valid, []byte(`{"second":true}`)...))
	require.ErrorContains(t, err, "multiple JSON values")

	withUnknown := append(valid[:len(valid)-1], []byte(`,"unknown":true}`)...)
	_, err = decodePodCheckpointManifest(withUnknown)
	require.ErrorContains(t, err, "unknown field")
}

func TestCheckpointNamesAreSafeAndScoped(t *testing.T) {
	archive := checkpointArchiveName("../../container")
	assert.Equal(t, filepath.Base(archive), archive)
	assert.NotContains(t, archive, "..")
	assert.NotEqual(t,
		restoreCheckpointImageName("sandbox-a", "container"),
		restoreCheckpointImageName("sandbox-b", "container"),
	)
}

func TestCheckpointRestoreRejectsSymlinkArtifacts(t *testing.T) {
	options, _, _ := writeRestoreFixture(t)
	archive := filepath.Join(options.CheckpointPath, checkpointArchiveName("old-container-id"))
	target := filepath.Join(t.TempDir(), "archive")
	require.NoError(t, os.WriteFile(target, []byte("outside"), 0o600))
	require.NoError(t, os.Remove(archive))
	require.NoError(t, os.Symlink(target, archive))

	_, err := prepareRestoreContainers(options)
	require.Error(t, err)
}

func TestPrepareRestoreContainersDoesNotReplayCheckpointCDIDevices(t *testing.T) {
	options, _, _ := writeRestoreFixture(t)
	manifest, err := readPodCheckpointManifest(options.CheckpointPath)
	require.NoError(t, err)
	checkpointConfig := new(runtime.ContainerConfig)
	require.NoError(t, json.Unmarshal(manifest.Containers[0].Config, checkpointConfig))
	checkpointConfig.CDIDevices = []*runtime.CDIDevice{{Name: "vendor.example/device=host-secret"}}
	manifest.Containers[0].Config, err = json.Marshal(checkpointConfig)
	require.NoError(t, err)
	writeJSONFile(t, filepath.Join(options.CheckpointPath, podCheckpointManifestFile), manifest)

	containers, err := prepareRestoreContainers(options)
	require.NoError(t, err)
	defer closeRestoreContainers(containers)
	require.Len(t, containers, 1)
	assert.Empty(t, containers[0].config.GetCDIDevices())
}

func TestReadPodCheckpointMetadataIsBoundedAndNoFollow(t *testing.T) {
	t.Run("manifest symlink", func(t *testing.T) {
		checkpointDir := t.TempDir()
		target := filepath.Join(t.TempDir(), "manifest")
		require.NoError(t, os.WriteFile(target, []byte("{}"), 0o600))
		require.NoError(t, os.Symlink(target, filepath.Join(checkpointDir, podCheckpointManifestFile)))

		_, err := readPodCheckpointManifest(checkpointDir)
		require.Error(t, err)
	})

	t.Run("oversized config", func(t *testing.T) {
		checkpointDir := t.TempDir()
		config := filepath.Join(checkpointDir, podCheckpointConfigFile)
		file, err := os.OpenFile(config, os.O_CREATE|os.O_WRONLY, 0o600)
		require.NoError(t, err)
		require.NoError(t, file.Truncate(maxPodCheckpointConfigSize+1))
		require.NoError(t, file.Close())

		_, err = readPodCheckpointConfig(checkpointDir)
		require.ErrorContains(t, err, "exceeds limit")
	})
}

func TestValidateCheckpointTarRejectsHostFilesystemEntries(t *testing.T) {
	tests := map[string]struct {
		header  tar.Header
		wantErr string
	}{
		"regular": {
			header: tar.Header{Name: "checkpoint/inventory.img", Mode: 0o600, Size: 1, Typeflag: tar.TypeReg},
		},
		"path traversal": {
			header:  tar.Header{Name: "../host", Mode: 0o600, Size: 1, Typeflag: tar.TypeReg},
			wantErr: "non-canonical path",
		},
		"absolute path": {
			header:  tar.Header{Name: "/host", Mode: 0o600, Size: 1, Typeflag: tar.TypeReg},
			wantErr: "invalid path",
		},
		"symlink": {
			header:  tar.Header{Name: "checkpoint/link", Linkname: "/host", Mode: 0o777, Typeflag: tar.TypeSymlink},
			wantErr: "forbidden type",
		},
		"device": {
			header:  tar.Header{Name: "checkpoint/device", Mode: 0o600, Typeflag: tar.TypeChar},
			wantErr: "forbidden type",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			var archive bytes.Buffer
			writer := tar.NewWriter(&archive)
			require.NoError(t, writer.WriteHeader(&test.header))
			if test.header.Size != 0 {
				_, err := writer.Write(bytes.Repeat([]byte{'x'}, int(test.header.Size)))
				require.NoError(t, err)
			}
			require.NoError(t, writer.Close())

			err := validateCheckpointTar(bytes.NewReader(archive.Bytes()), int64(archive.Len()))
			if test.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, test.wantErr)
		})
	}
}

func TestValidateCheckpointIndex(t *testing.T) {
	validIndex := func() imagespec.Index {
		descriptor := func(mediaType string) imagespec.Descriptor {
			return imagespec.Descriptor{MediaType: mediaType, Digest: digest.FromString(mediaType), Size: 1}
		}
		return imagespec.Index{
			Versioned: specs.Versioned{SchemaVersion: 2},
			Annotations: map[string]string{
				imagespec.AnnotationRefName: "base-image",
				checkpointRuntimeNameLabel:  "io.containerd.runc.v2",
				checkpointSnapshotterLabel:  "overlayfs",
			},
			Manifests: []imagespec.Descriptor{
				descriptor(images.MediaTypeContainerd1Checkpoint),
				descriptor(images.MediaTypeContainerd1CheckpointConfig),
				descriptor(images.MediaTypeContainerd1CheckpointOptions),
				descriptor(images.MediaTypeContainerd1CheckpointRuntimeOptions),
				descriptor(imagespec.MediaTypeImageLayerGzip),
			},
		}
	}

	index := validIndex()
	task, rw, err := validateCheckpointIndex(&index)
	require.NoError(t, err)
	assert.Equal(t, images.MediaTypeContainerd1Checkpoint, task.MediaType)
	assert.Equal(t, imagespec.MediaTypeImageLayerGzip, rw.MediaType)

	t.Run("task host path annotation", func(t *testing.T) {
		index := validIndex()
		index.Manifests[0].Annotations = map[string]string{"RestoreFromPath": "/host"}
		_, _, err := validateCheckpointIndex(&index)
		require.ErrorContains(t, err, "forbidden annotations")
	})

	t.Run("duplicate task descriptor", func(t *testing.T) {
		index := validIndex()
		index.Manifests = append(index.Manifests, index.Manifests[0])
		_, _, err := validateCheckpointIndex(&index)
		require.ErrorContains(t, err, "too many")
	})

	t.Run("unknown descriptor", func(t *testing.T) {
		index := validIndex()
		index.Manifests = append(index.Manifests, imagespec.Descriptor{
			MediaType: "application/vnd.example.host-control",
			Digest:    digest.FromString("unknown"),
			Size:      1,
		})
		_, _, err := validateCheckpointIndex(&index)
		require.ErrorContains(t, err, "unsupported descriptor")
	})

	t.Run("oversized metadata", func(t *testing.T) {
		index := validIndex()
		index.Manifests[1].Size = maxPodCheckpointManifestSize + 1
		_, _, err := validateCheckpointIndex(&index)
		require.ErrorContains(t, err, "outside the allowed range")
	})
}

func TestCheckpointArchiveNamesAreNeverImported(t *testing.T) {
	targetDigest := digest.FromString("checkpoint-index")
	index := &imagespec.Index{
		Versioned: specs.Versioned{SchemaVersion: 2},
		Manifests: []imagespec.Descriptor{{
			MediaType: imagespec.MediaTypeImageIndex,
			Digest:    targetDigest,
			Size:      42,
			Annotations: map[string]string{
				images.AnnotationImageName:  "registry.example/victim:latest",
				imagespec.AnnotationRefName: "victim:latest",
			},
		}},
	}
	target, err := checkpointTargetFromIndex(index)
	require.NoError(t, err)
	assert.Equal(t, targetDigest, target.Digest)
	assert.Empty(t, target.Annotations)

	t.Run("multiple image records", func(t *testing.T) {
		index := *index
		index.Manifests = append(append([]imagespec.Descriptor(nil), index.Manifests...), index.Manifests[0])
		_, err := checkpointTargetFromIndex(&index)
		require.ErrorContains(t, err, "instead of exactly one")
	})

	t.Run("control annotation", func(t *testing.T) {
		index := *index
		index.Manifests = append([]imagespec.Descriptor(nil), index.Manifests...)
		index.Manifests[0].Annotations = map[string]string{"RestoreFromPath": "/host"}
		_, err := checkpointTargetFromIndex(&index)
		require.ErrorContains(t, err, "unsupported annotation")
	})
}

func TestValidateRestoreImageConfigUsesImmutableDigest(t *testing.T) {
	checkpoint := digest.FromString("checkpoint-image-config")
	require.NoError(t, validateRestoreImageConfig(checkpoint, checkpoint))

	err := validateRestoreImageConfig(checkpoint, digest.FromString("different-image-config"))
	require.ErrorIs(t, err, errdefs.ErrFailedPrecondition)
	require.ErrorContains(t, err, "does not match checkpoint digest")
}

func TestRestoreContainerImageConfigDigestUsesCRIMetadata(t *testing.T) {
	expected := digest.FromString("resolved-image-config")
	metadataData, err := json.Marshal(&containerstore.Metadata{
		ID:       "new-container",
		ImageRef: expected.String(),
	})
	require.NoError(t, err)
	info := containers.Container{
		Extensions: map[string]typeurl.Any{
			crilabels.ContainerMetadataExtension: &anypb.Any{Value: metadataData},
		},
	}

	actual, err := restoreContainerImageConfigDigest(info, "new-container")
	require.NoError(t, err)
	assert.Equal(t, expected, actual)

	_, err = restoreContainerImageConfigDigest(info, "different-container")
	require.ErrorContains(t, err, "does not match container ID")
}

func writeRestoreFixture(t *testing.T) (sandbox.RestoreOptions, *runtime.PodSandboxConfig, *runtime.ContainerConfig) {
	t.Helper()
	checkpointDir := t.TempDir()
	sandboxConfig := &runtime.PodSandboxConfig{
		Metadata: &runtime.PodSandboxMetadata{Name: "pod", Namespace: "default", Uid: "old-uid", Attempt: 1},
		Hostname: "pod-hostname",
		Linux: &runtime.LinuxPodSandboxConfig{
			CgroupParent: "/old-cgroup",
			SecurityContext: &runtime.LinuxSandboxSecurityContext{
				NamespaceOptions: &runtime.NamespaceOption{},
			},
			Sysctls: map[string]string{"net.ipv4.ip_unprivileged_port_start": "0"},
		},
	}
	containerConfig := &runtime.ContainerConfig{
		Metadata:   &runtime.ContainerMetadata{Name: "app", Attempt: 1},
		Image:      &runtime.ImageSpec{Image: "registry.example/app:latest"},
		Command:    []string{"/app"},
		Args:       []string{"serve"},
		WorkingDir: "/work",
		Envs:       []*runtime.KeyValue{{Key: "MODE", Value: []byte("test")}},
		Linux: &runtime.LinuxContainerConfig{
			SecurityContext: &runtime.LinuxContainerSecurityContext{},
		},
	}
	status := &runtime.ContainerStatus{
		Id:       "old-container-id",
		Metadata: containerConfig.Metadata,
		State:    runtime.ContainerState_CONTAINER_RUNNING,
		ImageRef: digest.FromString("base-image").String(),
	}
	configData, err := json.Marshal(containerConfig)
	require.NoError(t, err)
	statusData, err := json.Marshal(status)
	require.NoError(t, err)
	writeJSONFile(t, filepath.Join(checkpointDir, podCheckpointConfigFile), sandboxConfig)
	writeJSONFile(t, filepath.Join(checkpointDir, podCheckpointManifestFile), podCheckpointManifest{
		Version:   podCheckpointManifestVersion,
		SandboxID: "old-sandbox-id",
		Containers: []podCheckpointManifestContainer{{
			Name:    "app",
			ID:      "old-container-id",
			Archive: checkpointArchiveName("old-container-id"),
			Config:  configData,
			Status:  statusData,
		}},
	})
	require.NoError(t, os.WriteFile(
		filepath.Join(checkpointDir, checkpointArchiveName("old-container-id")),
		[]byte("OCI checkpoint archive"),
		0o600,
	))

	restoreSandboxConfig := proto.Clone(sandboxConfig).(*runtime.PodSandboxConfig)
	restoreSandboxConfig.Metadata.Uid = "new-uid"
	restoreSandboxConfig.Metadata.Attempt++
	restoreSandboxConfig.Linux.CgroupParent = "/new-cgroup"
	sandboxAny, err := typeurl.MarshalAny(restoreSandboxConfig)
	require.NoError(t, err)
	containerAny, err := typeurl.MarshalAny(containerConfig)
	require.NoError(t, err)
	return sandbox.RestoreOptions{
		CheckpointPath: checkpointDir,
		SandboxConfig:  sandboxAny,
		Containers: []sandbox.RestoreContainer{{
			ID:     "new-container-id",
			Name:   "app",
			Config: containerAny,
		}},
	}, sandboxConfig, containerConfig
}

func writeJSONFile(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, data, 0o600))
}
