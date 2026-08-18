/*
   Copyright The containerd Authors.

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
*/

package server

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/containerd/errdefs"
	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	"github.com/containerd/containerd/v2/core/sandbox"
	containerstore "github.com/containerd/containerd/v2/internal/cri/store/container"
	imagestore "github.com/containerd/containerd/v2/internal/cri/store/image"
	sandboxstore "github.com/containerd/containerd/v2/internal/cri/store/sandbox"
)

type recordingCheckpointSandboxService struct {
	*fakeSandboxService
	opts sandbox.CheckpointOptions
}

func (s *recordingCheckpointSandboxService) CheckpointSandbox(_ context.Context, _, _ string, opts sandbox.CheckpointOptions) error {
	s.opts = opts
	return nil
}

func (s *recordingCheckpointSandboxService) RestoreSandbox(context.Context, string, sandbox.Sandbox, sandbox.RestoreOptions) (sandbox.RestoreResult, error) {
	return sandbox.RestoreResult{}, nil
}

type checkpointImageService struct {
	*fakeImageService
	image imagestore.Image
}

func (s *checkpointImageService) GetImage(string) (imagestore.Image, error) {
	return s.image, nil
}

type fakeCheckpointRestoreController struct {
	*fakeSandboxController
}

type fakeStagedRestoreController struct {
	*fakeCheckpointRestoreController
	supportErr error
}

func (f *fakeStagedRestoreController) SupportsStagedRestore(context.Context, string) error {
	return f.supportErr
}

func (*fakeStagedRestoreController) PrepareRestore(context.Context, sandbox.Sandbox, sandbox.RestoreOptions) (sandbox.ControllerInstance, error) {
	return sandbox.ControllerInstance{}, nil
}

func (*fakeStagedRestoreController) CompleteRestore(context.Context, sandbox.Sandbox, sandbox.RestoreOptions) ([]sandbox.RestoredTask, error) {
	return nil, nil
}

func (*fakeCheckpointRestoreController) Checkpoint(context.Context, string, sandbox.CheckpointOptions) error {
	return nil
}

func (*fakeCheckpointRestoreController) Restore(context.Context, sandbox.Sandbox, sandbox.RestoreOptions) (sandbox.RestoreResult, error) {
	return sandbox.RestoreResult{}, nil
}

func TestCheckpointPodRejectsActiveExec(t *testing.T) {
	c := newTestCRIService()
	const sandboxID = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	restoredSandbox := sandboxstore.NewSandbox(
		sandboxstore.Metadata{ID: sandboxID},
		sandboxstore.Status{State: sandboxstore.StateReady},
	)
	require.NoError(t, c.sandboxStore.Add(restoredSandbox))
	c.beginSandboxExec(sandboxID)
	defer c.endSandboxExec(sandboxID)

	_, err := c.CheckpointPod(context.Background(), &runtime.CheckpointPodRequest{
		PodSandboxId: sandboxID,
		OutputPath:   t.TempDir(),
		ContainerIds: []string{"container"},
	})
	require.Equal(t, codes.FailedPrecondition, status.Code(err))
}

func TestCheckpointPodRequiresAllRunningContainers(t *testing.T) {
	c := newTestCRIService()
	recorder := &recordingCheckpointSandboxService{fakeSandboxService: &fakeSandboxService{}}
	c.sandboxService = recorder
	c.ImageService = &checkpointImageService{fakeImageService: &fakeImageService{}}

	const sandboxID = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	restoredSandbox := sandboxstore.NewSandbox(
		sandboxstore.Metadata{ID: sandboxID},
		sandboxstore.Status{State: sandboxstore.StateReady},
	)
	require.NoError(t, c.sandboxStore.Add(restoredSandbox))

	for _, container := range []struct {
		id   string
		name string
	}{
		{id: "selected-container", name: "app"},
		{id: "unselected-container", name: "debugger"},
	} {
		stored, err := containerstore.NewContainer(containerstore.Metadata{
			ID:        container.id,
			SandboxID: sandboxID,
			ImageRef:  "test-image",
			Config: &runtime.ContainerConfig{
				Metadata: &runtime.ContainerMetadata{Name: container.name},
				Mounts: []*runtime.Mount{{
					ContainerPath: "/data",
					HostPath:      "/var/lib/kubelet/pods/test/volumes/kubernetes.io~csi/pvc/mount",
				}},
			},
		}, containerstore.WithFakeStatus(containerstore.Status{CreatedAt: 1, StartedAt: 2}))
		require.NoError(t, err)
		require.NoError(t, c.containerStore.Add(stored))
	}

	_, err := c.CheckpointPod(context.Background(), &runtime.CheckpointPodRequest{
		PodSandboxId: sandboxID,
		OutputPath:   t.TempDir(),
		ContainerIds: []string{"selected-container"},
		Options:      map[string]string{"runtime.example/format": "test"},
	})
	require.Equal(t, codes.FailedPrecondition, status.Code(err))

	_, err = c.CheckpointPod(context.Background(), &runtime.CheckpointPodRequest{
		PodSandboxId: sandboxID,
		OutputPath:   t.TempDir(),
		ContainerIds: []string{"selected-container", "unselected-container"},
		Options:      map[string]string{"runtime.example/format": "test"},
	})
	require.NoError(t, err)
	require.Equal(t, []sandbox.CheckpointTask{
		{CheckpointKey: "app", TaskID: "selected-container"},
		{CheckpointKey: "debugger", TaskID: "unselected-container"},
	}, recorder.opts.Tasks)
	require.Equal(t, map[string]string{"runtime.example/format": "test"}, recorder.opts.Options)
}

func TestValidateRestoreResult(t *testing.T) {
	plans := []restoreContainerPlan{{name: "app", id: "id-app"}, {name: "sidecar", id: "id-sidecar"}}
	validController := sandbox.ControllerInstance{Address: "ttrpc+unix:///run/test.sock", Version: 3, CreatedAt: time.Now()}

	tests := []struct {
		name      string
		tasks     []sandbox.RestoredTask
		wantError bool
	}{
		{name: "valid", tasks: []sandbox.RestoredTask{{CheckpointKey: "sidecar", TaskID: "id-sidecar"}, {CheckpointKey: "app", TaskID: "id-app"}}},
		{name: "missing", tasks: []sandbox.RestoredTask{{CheckpointKey: "app", TaskID: "id-app"}}, wantError: true},
		{name: "unknown key", tasks: []sandbox.RestoredTask{{CheckpointKey: "other", TaskID: "id-app"}, {CheckpointKey: "sidecar", TaskID: "id-sidecar"}}, wantError: true},
		{name: "wrong preallocated id", tasks: []sandbox.RestoredTask{{CheckpointKey: "app", TaskID: "source-id"}, {CheckpointKey: "sidecar", TaskID: "id-sidecar"}}, wantError: true},
		{name: "duplicate", tasks: []sandbox.RestoredTask{{CheckpointKey: "app", TaskID: "id-app"}, {CheckpointKey: "app", TaskID: "id-app"}}, wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateRestoreResult(plans, sandbox.RestoreResult{Controller: validController, Tasks: test.tasks})
			if test.wantError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestValidateRestoreSupport(t *testing.T) {
	ctx := context.Background()
	_, err := validateRestoreSupport(ctx, &fakeSandboxController{}, "podsandbox", "runtime", true)
	require.Equal(t, codes.Unimplemented, status.Code(err))
	_, err = validateRestoreSupport(
		ctx, &fakeCheckpointRestoreController{fakeSandboxController: &fakeSandboxController{}}, "shim", "runtime", false,
	)
	require.Equal(t, codes.Unimplemented, status.Code(err))
	staged, err := validateRestoreSupport(
		ctx, &fakeCheckpointRestoreController{fakeSandboxController: &fakeSandboxController{}}, "shim", "runtime", true,
	)
	require.NoError(t, err)
	require.False(t, staged)
	staged, err = validateRestoreSupport(ctx, &fakeStagedRestoreController{
		fakeCheckpointRestoreController: &fakeCheckpointRestoreController{fakeSandboxController: &fakeSandboxController{}},
	}, "shim", "runtime", false)
	require.NoError(t, err)
	require.True(t, staged)
	_, err = validateRestoreSupport(ctx, &fakeStagedRestoreController{
		fakeCheckpointRestoreController: &fakeCheckpointRestoreController{fakeSandboxController: &fakeSandboxController{}},
		supportErr:                      errdefs.ErrNotImplemented,
	}, "shim", "runtime", false)
	require.Equal(t, codes.Unimplemented, status.Code(err))
}

func TestValidateRestorePodRequestBeforeSideEffects(t *testing.T) {
	c := &criService{}
	tests := []struct {
		name string
		req  *runtime.RestorePodRequest
		code codes.Code
	}{
		{name: "nil", code: codes.InvalidArgument},
		{name: "reserved phase option", req: &runtime.RestorePodRequest{Options: map[string]string{sandbox.RestorePhaseOption: sandbox.RestorePhasePrepare}}, code: codes.InvalidArgument},
		{name: "relative path", req: &runtime.RestorePodRequest{CheckpointPath: "relative"}, code: codes.InvalidArgument},
		{name: "not found", req: &runtime.RestorePodRequest{CheckpointPath: filepath.Join(t.TempDir(), "missing")}, code: codes.NotFound},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, _, _, err := c.validateRestorePodRequest(test.req)
			require.Equal(t, test.code, status.Code(err))
		})
	}
}

func TestValidateRestorePodRequestAllowsRuntimeOptionsAndVolumeMounts(t *testing.T) {
	c := newTestCRIService()
	_, plans, _, err := c.validateRestorePodRequest(&runtime.RestorePodRequest{
		CheckpointPath: t.TempDir(),
		Config: &runtime.PodSandboxConfig{
			Metadata: &runtime.PodSandboxMetadata{Name: "restored", Namespace: "default", Uid: "pod-uid"},
		},
		ContainerConfigs: []*runtime.ContainerConfig{{
			Metadata: &runtime.ContainerMetadata{Name: "app"},
			Image:    &runtime.ImageSpec{Image: "example.invalid/image:test"},
			Mounts: []*runtime.Mount{{
				ContainerPath:  "/data",
				HostPath:       "/var/lib/kubelet/pods/test/volumes/kubernetes.io~csi/pvc/mount",
				Readonly:       true,
				SelinuxRelabel: true,
			}},
		}},
		Options: map[string]string{"runtime.example/format": "test"},
	})
	require.NoError(t, err)
	require.Len(t, plans, 1)
	require.Equal(t, "/data", plans[0].config.GetMounts()[0].GetContainerPath())
	require.Equal(t, "/var/lib/kubelet/pods/test/volumes/kubernetes.io~csi/pvc/mount", plans[0].config.GetMounts()[0].GetHostPath())
	require.True(t, plans[0].config.GetMounts()[0].GetReadonly())
	require.True(t, plans[0].config.GetMounts()[0].GetSelinuxRelabel())
}

func TestValidateCheckpointOutputPath(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, validateCheckpointOutputPath(dir))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "artifact"), []byte("x"), 0o600))
	require.Error(t, validateCheckpointOutputPath(dir))
	require.Error(t, validateCheckpointOutputPath("relative"))
}

func TestCheckpointOutputPathDetectsReplacement(t *testing.T) {
	parent := t.TempDir()
	path := filepath.Join(parent, "checkpoint")
	replaced := filepath.Join(parent, "checkpoint-replaced")
	require.NoError(t, os.Mkdir(path, 0o700))
	output, err := openCheckpointOutputPath(path)
	require.NoError(t, err)
	defer output.dir.Close()

	require.NoError(t, os.Rename(path, replaced))
	require.NoError(t, os.Mkdir(path, 0o700))
	require.Error(t, output.validateIdentity())
}

func TestRestorePodPlatformUsesImageMetadata(t *testing.T) {
	amd64 := imagespec.Platform{OS: "linux", Architecture: "amd64"}
	arm64 := imagespec.Platform{OS: "linux", Architecture: "arm64", Variant: "v8"}

	platform, err := restorePodPlatform([]restoreContainerPlan{
		{name: "app", image: imagestore.Image{ImageSpec: imagespec.Image{Platform: amd64}}},
		{name: "sidecar", image: imagestore.Image{ImageSpec: imagespec.Image{Platform: amd64}}},
	})
	require.NoError(t, err)
	require.Equal(t, amd64, platform)

	_, err = restorePodPlatform([]restoreContainerPlan{
		{name: "app", image: imagestore.Image{ImageSpec: imagespec.Image{Platform: amd64}}},
		{name: "sidecar", image: imagestore.Image{ImageSpec: imagespec.Image{Platform: arm64}}},
	})
	require.Error(t, err)
}

func TestValidatePodCheckpointRestoreImageRejectsImageDefinedVolumes(t *testing.T) {
	require.NoError(t, validatePodCheckpointRestoreImage(imagestore.Image{}))
	require.Error(t, validatePodCheckpointRestoreImage(imagestore.Image{ImageSpec: imagespec.Image{
		Config: imagespec.ImageConfig{Volumes: map[string]struct{}{`/data`: {}}},
	}}))
}

func TestValidatePodCheckpointRestoreContainerConfig(t *testing.T) {
	hostsPath := filepath.Join(t.TempDir(), "etc-hosts")
	terminationPath := filepath.Join(t.TempDir(), "termination-log")
	require.NoError(t, os.WriteFile(hostsPath, []byte("127.0.0.1 localhost\n"), 0o600))
	require.NoError(t, os.WriteFile(terminationPath, nil, 0o600))
	require.NoError(t, validatePodCheckpointRestoreContainerConfig(&runtime.ContainerConfig{}))
	require.NoError(t, validatePodCheckpointRestoreContainerConfig(&runtime.ContainerConfig{Linux: &runtime.LinuxContainerConfig{
		SecurityContext: &runtime.LinuxContainerSecurityContext{NamespaceOptions: &runtime.NamespaceOption{
			UsernsOptions: &runtime.UserNamespace{Mode: runtime.NamespaceMode_NODE},
		}},
	}}))
	require.Error(t, validatePodCheckpointRestoreContainerConfig(&runtime.ContainerConfig{Linux: &runtime.LinuxContainerConfig{
		SecurityContext: &runtime.LinuxContainerSecurityContext{NamespaceOptions: &runtime.NamespaceOption{
			UsernsOptions: &runtime.UserNamespace{Mode: runtime.NamespaceMode_POD},
		}},
	}}))
	require.NoError(t, validatePodCheckpointRestoreContainerConfig(&runtime.ContainerConfig{Mounts: []*runtime.Mount{{ContainerPath: "/etc/hosts", HostPath: hostsPath}}}))
	require.NoError(t, validatePodCheckpointRestoreContainerConfig(&runtime.ContainerConfig{
		Annotations: map[string]string{terminationMessagePathAnnotation: "/dev/termination-log"},
		Mounts: []*runtime.Mount{
			{ContainerPath: "/etc/hosts", HostPath: hostsPath},
			{ContainerPath: "/dev/termination-log", HostPath: terminationPath},
		},
	}))
	require.Error(t, validatePodCheckpointRestoreContainerConfig(&runtime.ContainerConfig{Mounts: []*runtime.Mount{{ContainerPath: "/data"}}}))
	require.Error(t, validatePodCheckpointRestoreContainerConfig(&runtime.ContainerConfig{Mounts: []*runtime.Mount{{ContainerPath: "/etc/hosts", HostPath: "relative"}}}))
	require.NoError(t, validatePodCheckpointRestoreContainerConfig(&runtime.ContainerConfig{Mounts: []*runtime.Mount{{ContainerPath: "/dev/termination-log", HostPath: terminationPath}}}))
	require.NoError(t, validatePodCheckpointRestoreContainerConfig(&runtime.ContainerConfig{
		Annotations: map[string]string{terminationMessagePathAnnotation: "/different-path"},
		Mounts:      []*runtime.Mount{{ContainerPath: "/dev/termination-log", HostPath: terminationPath}},
	}))
	require.Error(t, validatePodCheckpointRestoreContainerConfig(&runtime.ContainerConfig{
		Annotations: map[string]string{terminationMessagePathAnnotation: "/dev/termination-log"},
		Mounts:      []*runtime.Mount{{ContainerPath: "/dev/termination-log", HostPath: "relative"}},
	}))
	require.Error(t, validatePodCheckpointRestoreContainerConfig(&runtime.ContainerConfig{
		Annotations: map[string]string{terminationMessagePathAnnotation: "/dev/termination-log"},
		Mounts:      []*runtime.Mount{{ContainerPath: "/dev/termination-log", HostPath: terminationPath, Readonly: true}},
	}))
	require.NoError(t, validatePodCheckpointRestoreContainerConfig(&runtime.ContainerConfig{Devices: []*runtime.Device{{ContainerPath: "/dev/test"}}}))
	require.NoError(t, validatePodCheckpointRestoreContainerConfig(&runtime.ContainerConfig{CDIDevices: []*runtime.CDIDevice{{Name: "vendor/device=one"}}}))
}

func TestValidatePodCheckpointRestoreContainerConfigAllowsOrdinaryVolumeMounts(t *testing.T) {
	const podUID = "4bdf7d8c-78c9-4a9b-8990-13342d74ec7c"
	emptyDir := filepath.Join(t.TempDir(), "pods", podUID, "volumes", "kubernetes.io~empty-dir", "work")
	require.NoError(t, os.MkdirAll(emptyDir, 0o700))

	config := &runtime.ContainerConfig{
		Labels: map[string]string{"io.kubernetes.pod.uid": podUID},
		Mounts: []*runtime.Mount{{ContainerPath: "/data", HostPath: emptyDir}},
	}
	require.NoError(t, validatePodCheckpointRestoreContainerConfig(config))

	imageVolume := &runtime.ContainerConfig{Mounts: []*runtime.Mount{{
		ContainerPath: "/image-data",
		Image:         &runtime.ImageSpec{Image: "sha256:test-image-volume"},
		ImageSubPath:  "data",
		Readonly:      true,
	}}}
	require.NoError(t, validatePodCheckpointRestoreContainerConfig(imageVolume))

	imageVolume.Mounts[0].HostPath = emptyDir
	require.Error(t, validatePodCheckpointRestoreContainerConfig(imageVolume))
	imageVolume.Mounts[0].HostPath = ""
	imageVolume.Mounts[0].Image.Image = ""
	require.Error(t, validatePodCheckpointRestoreContainerConfig(imageVolume))
}

func TestToCRIErrorDefaultsToInternal(t *testing.T) {
	require.Equal(t, codes.Internal, status.Code(toCRIError(os.ErrInvalid)))
}

func TestStripUntrustedRestoreLabels(t *testing.T) {
	labels := map[string]string{restoredTaskSourceLabel: restoredTaskSourceValue, "user.example/key": "kept"}
	stripUntrustedRestoreLabels(labels)
	require.Equal(t, map[string]string{"user.example/key": "kept"}, labels)
}

func TestPrepareRestoredContainerNamespaces(t *testing.T) {
	spec := &runtimespec.Spec{Linux: &runtimespec.Linux{Namespaces: []runtimespec.LinuxNamespace{
		{Type: runtimespec.NetworkNamespace, Path: "/proc/0/ns/net"},
		{Type: runtimespec.IPCNamespace, Path: "/proc/0/ns/ipc"},
		{Type: runtimespec.UTSNamespace, Path: "/proc/0/ns/uts"},
		{Type: runtimespec.PIDNamespace, Path: "/proc/0/ns/pid"},
		{Type: runtimespec.MountNamespace},
	}}}

	prepareRestoredContainerNamespaces(spec, "/var/run/netns/restored")

	require.Equal(t, "/var/run/netns/restored", spec.Linux.Namespaces[0].Path)
	for _, index := range []int{1, 2, 3} {
		require.Empty(t, spec.Linux.Namespaces[index].Path)
	}
	require.Empty(t, spec.Linux.Namespaces[4].Path)
}
