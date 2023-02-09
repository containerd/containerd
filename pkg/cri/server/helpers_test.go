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

package server

import (
	"context"
	"os"
	goruntime "runtime"
	"strings"
	"testing"
	"time"

	"github.com/containerd/containerd/containers"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/oci"
	criconfig "github.com/containerd/containerd/pkg/cri/config"
	containerstore "github.com/containerd/containerd/pkg/cri/store/container"
	imagestore "github.com/containerd/containerd/pkg/cri/store/image"
	"github.com/containerd/containerd/plugin"
	"github.com/containerd/containerd/protobuf/types"
	"github.com/containerd/containerd/reference/docker"
	"github.com/containerd/containerd/runtime/linux/runctypes"
	runcoptions "github.com/containerd/containerd/runtime/v2/runc/options"
	"github.com/containerd/typeurl/v2"

	imagedigest "github.com/opencontainers/go-digest"
	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/pelletier/go-toml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

// TestGetUserFromImage tests the logic of getting image uid or user name of image user.
func TestGetUserFromImage(t *testing.T) {
	newI64 := func(i int64) *int64 { return &i }
	for c, test := range map[string]struct {
		user string
		uid  *int64
		name string
	}{
		"no gid": {
			user: "0",
			uid:  newI64(0),
		},
		"uid/gid": {
			user: "0:1",
			uid:  newI64(0),
		},
		"empty user": {
			user: "",
		},
		"multiple separators": {
			user: "1:2:3",
			uid:  newI64(1),
		},
		"root username": {
			user: "root:root",
			name: "root",
		},
		"username": {
			user: "test:test",
			name: "test",
		},
	} {
		t.Run(c, func(t *testing.T) {
			actualUID, actualName := getUserFromImage(test.user)
			assert.Equal(t, test.uid, actualUID)
			assert.Equal(t, test.name, actualName)
		})
	}
}

func TestGetRepoDigestAndTag(t *testing.T) {
	digest := imagedigest.Digest("sha256:e6693c20186f837fc393390135d8a598a96a833917917789d63766cab6c59582")
	for desc, test := range map[string]struct {
		ref                string
		schema1            bool
		expectedRepoDigest string
		expectedRepoTag    string
	}{
		"repo tag should be empty if original ref has no tag": {
			ref:                "gcr.io/library/busybox@" + digest.String(),
			expectedRepoDigest: "gcr.io/library/busybox@" + digest.String(),
		},
		"repo tag should not be empty if original ref has tag": {
			ref:                "gcr.io/library/busybox:latest",
			expectedRepoDigest: "gcr.io/library/busybox@" + digest.String(),
			expectedRepoTag:    "gcr.io/library/busybox:latest",
		},
		"repo digest should be empty if original ref is schema1 and has no digest": {
			ref:                "gcr.io/library/busybox:latest",
			schema1:            true,
			expectedRepoDigest: "",
			expectedRepoTag:    "gcr.io/library/busybox:latest",
		},
		"repo digest should not be empty if original ref is schema1 but has digest": {
			ref:                "gcr.io/library/busybox@sha256:e6693c20186f837fc393390135d8a598a96a833917917789d63766cab6c59594",
			schema1:            true,
			expectedRepoDigest: "gcr.io/library/busybox@sha256:e6693c20186f837fc393390135d8a598a96a833917917789d63766cab6c59594",
			expectedRepoTag:    "",
		},
	} {
		t.Run(desc, func(t *testing.T) {
			named, err := docker.ParseDockerRef(test.ref)
			assert.NoError(t, err)
			repoDigest, repoTag := getRepoDigestAndTag(named, digest, test.schema1)
			assert.Equal(t, test.expectedRepoDigest, repoDigest)
			assert.Equal(t, test.expectedRepoTag, repoTag)
		})
	}
}

func TestBuildLabels(t *testing.T) {
	imageConfigLabels := map[string]string{
		"a":          "z",
		"d":          "y",
		"long-label": strings.Repeat("example", 10000),
	}
	configLabels := map[string]string{
		"a": "b",
		"c": "d",
	}
	newLabels := buildLabels(configLabels, imageConfigLabels, containerKindSandbox)
	assert.Len(t, newLabels, 4)
	assert.Equal(t, "b", newLabels["a"])
	assert.Equal(t, "d", newLabels["c"])
	assert.Equal(t, "y", newLabels["d"])
	assert.Equal(t, containerKindSandbox, newLabels[containerKindLabel])
	assert.NotContains(t, newLabels, "long-label")

	newLabels["a"] = "e"
	assert.Empty(t, configLabels[containerKindLabel], "should not add new labels into original label")
	assert.Equal(t, "b", configLabels["a"], "change in new labels should not affect original label")
}

func TestParseImageReferences(t *testing.T) {
	refs := []string{
		"gcr.io/library/busybox@sha256:e6693c20186f837fc393390135d8a598a96a833917917789d63766cab6c59582",
		"gcr.io/library/busybox:1.2",
		"sha256:e6693c20186f837fc393390135d8a598a96a833917917789d63766cab6c59582",
		"arbitrary-ref",
	}
	expectedTags := []string{
		"gcr.io/library/busybox:1.2",
	}
	expectedDigests := []string{"gcr.io/library/busybox@sha256:e6693c20186f837fc393390135d8a598a96a833917917789d63766cab6c59582"}
	tags, digests := parseImageReferences(refs)
	assert.Equal(t, expectedTags, tags)
	assert.Equal(t, expectedDigests, digests)
}

func TestLocalResolve(t *testing.T) {
	image := imagestore.Image{
		ID:      "sha256:c75bebcdd211f41b3a460c7bf82970ed6c75acaab9cd4c9a4e125b03ca113799",
		ChainID: "test-chain-id-1",
		References: []string{
			"docker.io/library/busybox:latest",
			"docker.io/library/busybox@sha256:e6693c20186f837fc393390135d8a598a96a833917917789d63766cab6c59582",
		},
		Size: 10,
	}
	c := newTestCRIService()
	var err error
	c.imageStore, err = imagestore.NewFakeStore([]imagestore.Image{image})
	assert.NoError(t, err)

	for _, ref := range []string{
		"sha256:c75bebcdd211f41b3a460c7bf82970ed6c75acaab9cd4c9a4e125b03ca113799",
		"busybox",
		"busybox:latest",
		"busybox@sha256:e6693c20186f837fc393390135d8a598a96a833917917789d63766cab6c59582",
		"library/busybox",
		"library/busybox:latest",
		"library/busybox@sha256:e6693c20186f837fc393390135d8a598a96a833917917789d63766cab6c59582",
		"docker.io/busybox",
		"docker.io/busybox:latest",
		"docker.io/busybox@sha256:e6693c20186f837fc393390135d8a598a96a833917917789d63766cab6c59582",
		"docker.io/library/busybox",
		"docker.io/library/busybox:latest",
		"docker.io/library/busybox@sha256:e6693c20186f837fc393390135d8a598a96a833917917789d63766cab6c59582",
	} {
		img, err := c.localResolve(ref)
		assert.NoError(t, err)
		assert.Equal(t, image, img)
	}
	img, err := c.localResolve("randomid")
	assert.Equal(t, errdefs.IsNotFound(err), true)
	assert.Equal(t, imagestore.Image{}, img)
}

func TestGenerateRuntimeOptions(t *testing.T) {
	nilOpts := `
systemd_cgroup = true
[containerd]
  no_pivot = true
  default_runtime_name = "default"
[containerd.runtimes.legacy]
  runtime_type = "` + plugin.RuntimeLinuxV1 + `"
[containerd.runtimes.runc]
  runtime_type = "` + plugin.RuntimeRuncV1 + `"
[containerd.runtimes.runcv2]
  runtime_type = "` + plugin.RuntimeRuncV2 + `"
`
	nonNilOpts := `
systemd_cgroup = true
[containerd]
  no_pivot = true
  default_runtime_name = "default"
[containerd.runtimes.legacy]
  runtime_type = "` + plugin.RuntimeLinuxV1 + `"
[containerd.runtimes.legacy.options]
  Runtime = "legacy"
  RuntimeRoot = "/legacy"
[containerd.runtimes.runc]
  runtime_type = "` + plugin.RuntimeRuncV1 + `"
[containerd.runtimes.runc.options]
  BinaryName = "runc"
  Root = "/runc"
  NoNewKeyring = true
[containerd.runtimes.runcv2]
  runtime_type = "` + plugin.RuntimeRuncV2 + `"
[containerd.runtimes.runcv2.options]
  BinaryName = "runc"
  Root = "/runcv2"
  NoNewKeyring = true
`
	var nilOptsConfig, nonNilOptsConfig criconfig.Config
	tree, err := toml.Load(nilOpts)
	require.NoError(t, err)
	err = tree.Unmarshal(&nilOptsConfig)
	require.NoError(t, err)
	require.Len(t, nilOptsConfig.Runtimes, 3)

	tree, err = toml.Load(nonNilOpts)
	require.NoError(t, err)
	err = tree.Unmarshal(&nonNilOptsConfig)
	require.NoError(t, err)
	require.Len(t, nonNilOptsConfig.Runtimes, 3)

	for desc, test := range map[string]struct {
		r               criconfig.Runtime
		c               criconfig.Config
		expectedOptions interface{}
	}{
		"when options is nil, should return nil option for io.containerd.runc.v1": {
			r:               nilOptsConfig.Runtimes["runc"],
			c:               nilOptsConfig,
			expectedOptions: nil,
		},
		"when options is nil, should return nil option for io.containerd.runc.v2": {
			r:               nilOptsConfig.Runtimes["runcv2"],
			c:               nilOptsConfig,
			expectedOptions: nil,
		},
		"when options is nil, should use legacy fields for legacy runtime": {
			r: nilOptsConfig.Runtimes["legacy"],
			c: nilOptsConfig,
			expectedOptions: &runctypes.RuncOptions{
				SystemdCgroup: true,
			},
		},
		"when options is not nil, should be able to decode for io.containerd.runc.v1": {
			r: nonNilOptsConfig.Runtimes["runc"],
			c: nonNilOptsConfig,
			expectedOptions: &runcoptions.Options{
				BinaryName:   "runc",
				Root:         "/runc",
				NoNewKeyring: true,
			},
		},
		"when options is not nil, should be able to decode for io.containerd.runc.v2": {
			r: nonNilOptsConfig.Runtimes["runcv2"],
			c: nonNilOptsConfig,
			expectedOptions: &runcoptions.Options{
				BinaryName:   "runc",
				Root:         "/runcv2",
				NoNewKeyring: true,
			},
		},
		"when options is not nil, should be able to decode for legacy runtime": {
			r: nonNilOptsConfig.Runtimes["legacy"],
			c: nonNilOptsConfig,
			expectedOptions: &runctypes.RuncOptions{
				Runtime:     "legacy",
				RuntimeRoot: "/legacy",
			},
		},
	} {
		t.Run(desc, func(t *testing.T) {
			opts, err := generateRuntimeOptions(test.r, test.c)
			assert.NoError(t, err)
			assert.Equal(t, test.expectedOptions, opts)
		})
	}
}

func TestEnvDeduplication(t *testing.T) {
	for desc, test := range map[string]struct {
		existing []string
		kv       [][2]string
		expected []string
	}{
		"single env": {
			kv: [][2]string{
				{"a", "b"},
			},
			expected: []string{"a=b"},
		},
		"multiple envs": {
			kv: [][2]string{
				{"a", "b"},
				{"c", "d"},
				{"e", "f"},
			},
			expected: []string{
				"a=b",
				"c=d",
				"e=f",
			},
		},
		"env override": {
			kv: [][2]string{
				{"k1", "v1"},
				{"k2", "v2"},
				{"k3", "v3"},
				{"k3", "v4"},
				{"k1", "v5"},
				{"k4", "v6"},
			},
			expected: []string{
				"k1=v5",
				"k2=v2",
				"k3=v4",
				"k4=v6",
			},
		},
		"existing env": {
			existing: []string{
				"k1=v1",
				"k2=v2",
				"k3=v3",
			},
			kv: [][2]string{
				{"k3", "v4"},
				{"k2", "v5"},
				{"k4", "v6"},
			},
			expected: []string{
				"k1=v1",
				"k2=v5",
				"k3=v4",
				"k4=v6",
			},
		},
	} {
		t.Run(desc, func(t *testing.T) {
			var spec runtimespec.Spec
			if len(test.existing) > 0 {
				spec.Process = &runtimespec.Process{
					Env: test.existing,
				}
			}
			for _, kv := range test.kv {
				oci.WithEnv([]string{kv[0] + "=" + kv[1]})(context.Background(), nil, nil, &spec)
			}
			assert.Equal(t, test.expected, spec.Process.Env)
		})
	}
}

func TestPassThroughAnnotationsFilter(t *testing.T) {
	for desc, test := range map[string]struct {
		podAnnotations         map[string]string
		runtimePodAnnotations  []string
		passthroughAnnotations map[string]string
	}{
		"should support direct match": {
			podAnnotations:         map[string]string{"c": "d", "d": "e"},
			runtimePodAnnotations:  []string{"c"},
			passthroughAnnotations: map[string]string{"c": "d"},
		},
		"should support wildcard match": {
			podAnnotations: map[string]string{
				"t.f":  "j",
				"z.g":  "o",
				"z":    "o",
				"y.ca": "b",
				"y":    "b",
			},
			runtimePodAnnotations: []string{"*.f", "z*g", "y.c*"},
			passthroughAnnotations: map[string]string{
				"t.f":  "j",
				"z.g":  "o",
				"y.ca": "b",
			},
		},
		"should support wildcard match all": {
			podAnnotations: map[string]string{
				"t.f":  "j",
				"z.g":  "o",
				"z":    "o",
				"y.ca": "b",
				"y":    "b",
			},
			runtimePodAnnotations: []string{"*"},
			passthroughAnnotations: map[string]string{
				"t.f":  "j",
				"z.g":  "o",
				"z":    "o",
				"y.ca": "b",
				"y":    "b",
			},
		},
		"should support match including path separator": {
			podAnnotations: map[string]string{
				"matchend.com/end":    "1",
				"matchend.com/end1":   "2",
				"matchend.com/1end":   "3",
				"matchmid.com/mid":    "4",
				"matchmid.com/mi1d":   "5",
				"matchmid.com/mid1":   "6",
				"matchhead.com/head":  "7",
				"matchhead.com/1head": "8",
				"matchhead.com/head1": "9",
				"matchall.com/abc":    "10",
				"matchall.com/def":    "11",
				"end/matchend":        "12",
				"end1/matchend":       "13",
				"1end/matchend":       "14",
				"mid/matchmid":        "15",
				"mi1d/matchmid":       "16",
				"mid1/matchmid":       "17",
				"head/matchhead":      "18",
				"1head/matchhead":     "19",
				"head1/matchhead":     "20",
				"abc/matchall":        "21",
				"def/matchall":        "22",
				"match1/match2":       "23",
				"nomatch/nomatch":     "24",
			},
			runtimePodAnnotations: []string{
				"matchend.com/end*",
				"matchmid.com/mi*d",
				"matchhead.com/*head",
				"matchall.com/*",
				"end*/matchend",
				"mi*d/matchmid",
				"*head/matchhead",
				"*/matchall",
				"match*/match*",
			},
			passthroughAnnotations: map[string]string{
				"matchend.com/end":    "1",
				"matchend.com/end1":   "2",
				"matchmid.com/mid":    "4",
				"matchmid.com/mi1d":   "5",
				"matchhead.com/head":  "7",
				"matchhead.com/1head": "8",
				"matchall.com/abc":    "10",
				"matchall.com/def":    "11",
				"end/matchend":        "12",
				"end1/matchend":       "13",
				"mid/matchmid":        "15",
				"mi1d/matchmid":       "16",
				"head/matchhead":      "18",
				"1head/matchhead":     "19",
				"abc/matchall":        "21",
				"def/matchall":        "22",
				"match1/match2":       "23",
			},
		},
	} {
		t.Run(desc, func(t *testing.T) {
			passthroughAnnotations := getPassthroughAnnotations(test.podAnnotations, test.runtimePodAnnotations)
			assert.Equal(t, test.passthroughAnnotations, passthroughAnnotations)
		})
	}
}

func TestEnsureRemoveAllNotExist(t *testing.T) {
	// should never return an error for a non-existent path
	if err := ensureRemoveAll(context.Background(), "/non/existent/path"); err != nil {
		t.Fatal(err)
	}
}

func TestEnsureRemoveAllWithDir(t *testing.T) {
	dir := t.TempDir()
	if err := ensureRemoveAll(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
}

func TestEnsureRemoveAllWithFile(t *testing.T) {
	tmp, err := os.CreateTemp("", "test-ensure-removeall-with-dir")
	if err != nil {
		t.Fatal(err)
	}
	tmp.Close()
	if err := ensureRemoveAll(context.Background(), tmp.Name()); err != nil {
		t.Fatal(err)
	}
}

// Helper function for setting up an environment to test PID namespace targeting.
func addContainer(c *criService, containerID, sandboxID string, PID uint32, createdAt, startedAt, finishedAt int64) error {
	meta := containerstore.Metadata{
		ID:        containerID,
		SandboxID: sandboxID,
	}
	status := containerstore.Status{
		Pid:        PID,
		CreatedAt:  createdAt,
		StartedAt:  startedAt,
		FinishedAt: finishedAt,
	}
	container, err := containerstore.NewContainer(meta,
		containerstore.WithFakeStatus(status),
	)
	if err != nil {
		return err
	}
	return c.containerStore.Add(container)
}

func TestValidateTargetContainer(t *testing.T) {
	testSandboxID := "test-sandbox-uid"

	// The existing container that will be targeted.
	testTargetContainerID := "test-target-container"
	testTargetContainerPID := uint32(4567)

	// A container that has finished running and cannot be targeted.
	testStoppedContainerID := "stopped-target-container"
	testStoppedContainerPID := uint32(6789)

	// A container from another pod.
	testOtherContainerSandboxID := "other-sandbox-uid"
	testOtherContainerID := "other-target-container"
	testOtherContainerPID := uint32(7890)

	// Container create/start/stop times.
	createdAt := time.Now().Add(-15 * time.Second).UnixNano()
	startedAt := time.Now().Add(-10 * time.Second).UnixNano()
	finishedAt := time.Now().Add(-5 * time.Second).UnixNano()

	c := newTestCRIService()

	// Create a target container.
	err := addContainer(c, testTargetContainerID, testSandboxID, testTargetContainerPID, createdAt, startedAt, 0)
	require.NoError(t, err, "error creating test target container")

	// Create a stopped container.
	err = addContainer(c, testStoppedContainerID, testSandboxID, testStoppedContainerPID, createdAt, startedAt, finishedAt)
	require.NoError(t, err, "error creating test stopped container")

	// Create a container in another pod.
	err = addContainer(c, testOtherContainerID, testOtherContainerSandboxID, testOtherContainerPID, createdAt, startedAt, 0)
	require.NoError(t, err, "error creating test container in other pod")

	for desc, test := range map[string]struct {
		targetContainerID string
		expectError       bool
	}{
		"target container in pod": {
			targetContainerID: testTargetContainerID,
			expectError:       false,
		},
		"target stopped container in pod": {
			targetContainerID: testStoppedContainerID,
			expectError:       true,
		},
		"target container does not exist": {
			targetContainerID: "no-container-with-this-id",
			expectError:       true,
		},
		"target container in other pod": {
			targetContainerID: testOtherContainerID,
			expectError:       true,
		},
	} {
		t.Run(desc, func(t *testing.T) {
			targetContainer, err := c.validateTargetContainer(testSandboxID, test.targetContainerID)
			if test.expectError {
				require.Error(t, err, "target should have been invalid but no error")
				return
			}
			require.NoErrorf(t, err, "target should have been valid but got error")

			assert.Equal(t, test.targetContainerID, targetContainer.ID, "returned target container does not have expected ID")
		})
	}

}

func TestGetRuntimeOptions(t *testing.T) {
	_, err := getRuntimeOptions(containers.Container{})
	require.NoError(t, err)

	var pbany *types.Any               // This is nil.
	var typeurlAny typeurl.Any = pbany // This is typed nil.
	_, err = getRuntimeOptions(containers.Container{Runtime: containers.RuntimeInfo{Options: typeurlAny}})
	require.NoError(t, err)
}

func TestHostNetwork(t *testing.T) {
	tests := []struct {
		name     string
		c        *runtime.PodSandboxConfig
		expected bool
	}{
		{
			name: "when pod namespace return false",
			c: &runtime.PodSandboxConfig{
				Linux: &runtime.LinuxPodSandboxConfig{
					SecurityContext: &runtime.LinuxSandboxSecurityContext{
						NamespaceOptions: &runtime.NamespaceOption{
							Network: runtime.NamespaceMode_POD,
						},
					},
				},
			},
			expected: false,
		},
		{
			name: "when node namespace return true",
			c: &runtime.PodSandboxConfig{
				Linux: &runtime.LinuxPodSandboxConfig{
					SecurityContext: &runtime.LinuxSandboxSecurityContext{
						NamespaceOptions: &runtime.NamespaceOption{
							Network: runtime.NamespaceMode_NODE,
						},
					},
				},
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		if goruntime.GOOS != "linux" {
			t.Skip()
		}
		t.Run(tt.name, func(t *testing.T) {
			if hostNetwork(tt.c) != tt.expected {
				t.Errorf("failed hostNetwork got %t expected %t", hostNetwork(tt.c), tt.expected)
			}
		})
	}
}
