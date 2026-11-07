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
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/containerd/containerd/v2/integration/images"
	digest "github.com/opencontainers/go-digest"
	specs "github.com/opencontainers/image-spec/specs-go"
	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
	crierrors "k8s.io/cri-api/pkg/errors"
)

// TestSecurityProfileOCI exercises the actual CRI image and runtime endpoints and
// checks that the kernel enforces the overlay after its stored copy is removed.
func TestSecurityProfileOCI(t *testing.T) {
	ctx := context.Background()
	raw, conn, err := RawRuntimeClient()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })
	status, err := raw.Status(ctx, &runtime.StatusRequest{})
	require.NoError(t, err)
	require.True(t, status.GetFeatures().GetSeccompProfileOci())
	imageClient := runtime.NewImageServiceClient(conn)
	data := []byte(`{"defaultAction":"SCMP_ACT_ALLOW","syscalls":[{"names":["mkdir","mkdirat"],"action":"SCMP_ACT_ERRNO","errnoRet":1}]}`)
	layer := imagespec.Descriptor{MediaType: "application/octet-stream", Digest: digest.FromBytes(data), Size: int64(len(data))}
	manifest := imagespec.Manifest{Versioned: specs.Versioned{SchemaVersion: 2}, MediaType: imagespec.MediaTypeImageManifest, Config: imagespec.Descriptor{MediaType: "application/vnd.cncf.seccomp-profile.config.v1+json", Digest: digest.FromString("{}"), Size: 2}, Layers: []imagespec.Descriptor{layer}}
	manifestData, err := json.Marshal(manifest)
	require.NoError(t, err)
	manifestDigest := digest.FromBytes(manifestData)
	var requests atomic.Int32
	registry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		switch {
		case strings.Contains(r.URL.Path, "/manifests/"):
			w.Header().Set("Content-Type", imagespec.MediaTypeImageManifest)
			w.Header().Set("Docker-Content-Digest", manifestDigest.String())
			w.Header().Set("Content-Length", fmt.Sprint(len(manifestData)))
			if r.Method != "HEAD" {
				_, _ = w.Write(manifestData)
			}
		case strings.HasSuffix(r.URL.Path, digest.FromString("{}").String()):
			w.Header().Set("Content-Length", "2")
			_, _ = w.Write([]byte("{}"))
		case strings.Contains(r.URL.Path, "/blobs/"):
			_, _ = w.Write(data)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(registry.Close)
	ref := strings.TrimPrefix(registry.URL, "http://") + "/profile@" + manifestDigest.String()
	request := &runtime.PullSecurityProfileRequest{Image: &runtime.ImageSpec{Image: ref, RuntimeHandler: *runtimeHandler}, ProfileKind: runtime.SecurityProfileKind_Seccomp}
	response, err := imageClient.PullSecurityProfile(ctx, request)
	require.NoError(t, err)
	require.False(t, response.Cached)
	t.Cleanup(func() {
		_, err := imageClient.RemoveSecurityProfile(ctx, &runtime.RemoveSecurityProfileRequest{Digest: manifestDigest.String()})
		require.NoError(t, err)
	})
	before := requests.Load()
	registry.Close()
	response, err = imageClient.PullSecurityProfile(ctx, request)
	require.NoError(t, err)
	require.True(t, response.Cached)
	require.Equal(t, before, requests.Load())
	list, err := imageClient.ListSecurityProfiles(ctx, &runtime.ListSecurityProfilesRequest{})
	require.NoError(t, err)
	found := false
	for _, profile := range list.Profiles {
		if profile.Digest == manifestDigest.String() {
			found = true
			require.Contains(t, profile.Refs, ref)
		}
	}
	require.True(t, found)
	listedImages, err := imageService.ListImages(nil)
	require.NoError(t, err)
	for _, image := range listedImages {
		require.NotContains(t, image.RepoDigests, ref)
	}

	image := images.Get(images.BusyBox)
	pullImagesByCRI(t, imageService, image)
	podConfig := PodSandboxConfig("oci-security-profile", "seccomp", WithHostNetwork)
	podConfig.Linux.SecurityContext.Seccomp = &runtime.SecurityProfile{ProfileType: runtime.SecurityProfile_OCI, OciRef: ref}
	podID, err := runtimeService.RunPodSandbox(podConfig, *runtimeHandler)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, runtimeService.StopPodSandbox(podID))
		require.NoError(t, runtimeService.RemovePodSandbox(podID))
	})
	config := ContainerConfig("oci-profile", image, WithCommand("sleep", "300"))
	if config.Linux == nil {
		config.Linux = &runtime.LinuxContainerConfig{}
	}
	config.Linux.SecurityContext = &runtime.LinuxContainerSecurityContext{Seccomp: &runtime.SecurityProfile{ProfileType: runtime.SecurityProfile_OCI, OciRef: ref}}
	containerID, err := runtimeService.CreateContainer(podID, config, podConfig)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, runtimeService.StopContainer(containerID, 0))
		require.NoError(t, runtimeService.RemoveContainer(containerID))
	})
	require.NoError(t, runtimeService.StartContainer(containerID))
	assertRestricted := func() {
		response, err := raw.ExecSync(ctx, &runtime.ExecSyncRequest{ContainerId: containerID, Cmd: []string{"mkdir", "/tmp/oci-profile-denied"}, Timeout: 10})
		require.NoError(t, err)
		require.NotZero(t, response.ExitCode)
		require.Contains(t, string(response.Stderr), "Operation not permitted")
	}
	assertRestricted()
	_, err = imageClient.RemoveSecurityProfile(ctx, &runtime.RemoveSecurityProfileRequest{Digest: manifestDigest.String()})
	require.NoError(t, err)
	assertRestricted()
	config.Metadata.Name = "removed-profile"
	_, err = runtimeService.CreateContainer(podID, config, podConfig)
	require.Error(t, err)
	_, err = imageClient.PullSecurityProfile(ctx, request)
	require.ErrorContains(t, err, crierrors.ErrRegistryUnavailable.Error())
}
