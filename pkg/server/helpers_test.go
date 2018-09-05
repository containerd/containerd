/*
Copyright 2017 The Kubernetes Authors.

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
	"sort"
	"testing"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/containers"
	"github.com/containerd/containerd/runtime/linux/runctypes"
	imagedigest "github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/assert"
	"golang.org/x/net/context"
	runtime "k8s.io/kubernetes/pkg/kubelet/apis/cri/runtime/v1alpha2"

	criconfig "github.com/containerd/cri/pkg/config"
	"github.com/containerd/cri/pkg/util"
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
		"multiple spearators": {
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
		t.Logf("TestCase - %q", c)
		actualUID, actualName := getUserFromImage(test.user)
		assert.Equal(t, test.uid, actualUID)
		assert.Equal(t, test.name, actualName)
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
		"repo digest should not be empty if orignal ref is schema1 but has digest": {
			ref:                "gcr.io/library/busybox@sha256:e6693c20186f837fc393390135d8a598a96a833917917789d63766cab6c59594",
			schema1:            true,
			expectedRepoDigest: "gcr.io/library/busybox@sha256:e6693c20186f837fc393390135d8a598a96a833917917789d63766cab6c59594",
			expectedRepoTag:    "",
		},
	} {
		t.Logf("TestCase %q", desc)
		named, err := util.NormalizeImageRef(test.ref)
		assert.NoError(t, err)
		repoDigest, repoTag := getRepoDigestAndTag(named, digest, test.schema1)
		assert.Equal(t, test.expectedRepoDigest, repoDigest)
		assert.Equal(t, test.expectedRepoTag, repoTag)
	}
}

func TestGetCgroupsPath(t *testing.T) {
	testID := "test-id"
	for desc, test := range map[string]struct {
		cgroupsParent string
		systemdCgroup bool
		expected      string
	}{
		"should support regular cgroup path": {
			cgroupsParent: "/a/b",
			systemdCgroup: false,
			expected:      "/a/b/test-id",
		},
		"should support systemd cgroup path": {
			cgroupsParent: "/a.slice/b.slice",
			systemdCgroup: true,
			expected:      "b.slice:cri-containerd:test-id",
		},
	} {
		t.Logf("TestCase %q", desc)
		got := getCgroupsPath(test.cgroupsParent, testID, test.systemdCgroup)
		assert.Equal(t, test.expected, got)
	}
}

func TestBuildLabels(t *testing.T) {
	configLabels := map[string]string{
		"a": "b",
		"c": "d",
	}
	newLabels := buildLabels(configLabels, containerKindSandbox)
	assert.Len(t, newLabels, 3)
	assert.Equal(t, "b", newLabels["a"])
	assert.Equal(t, "d", newLabels["c"])
	assert.Equal(t, containerKindSandbox, newLabels[containerKindLabel])

	newLabels["a"] = "e"
	assert.Empty(t, configLabels[containerKindLabel], "should not add new labels into original label")
	assert.Equal(t, "b", configLabels["a"], "change in new labels should not affect original label")
}

func TestGetRuntimeConfigFromContainerInfo(t *testing.T) {
	for desc, test := range map[string]struct {
		typ             string
		engine          string
		root            string
		expectErr       bool
		expectedRuntime criconfig.Runtime
	}{
		"should return error if there is no runc options": {
			typ:       "test.type",
			expectErr: true,
		},
		"should retrieve runtime from container info": {
			typ:    "test.type",
			engine: "test-engine",
			root:   "/test/root",
			expectedRuntime: criconfig.Runtime{
				Type:   "test.type",
				Engine: "test-engine",
				Root:   "/test/root",
			},
		},
	} {
		t.Run(desc, func(t *testing.T) {
			var opts interface{}
			if test.engine != "" || test.root != "" {
				opts = &runctypes.RuncOptions{
					Runtime:     test.engine,
					RuntimeRoot: test.root,
				}
			}
			c := containers.Container{}
			assert.NoError(t, containerd.WithRuntime(
				test.typ,
				opts,
			)(context.Background(), nil, &c))
			r, err := getRuntimeConfigFromContainerInfo(c)
			assert.Equal(t, test.expectErr, err != nil)
			assert.Equal(t, test.expectedRuntime, r)
		})
	}
}

func TestOrderedMounts(t *testing.T) {
	mounts := []*runtime.Mount{
		{ContainerPath: "/a/b/c"},
		{ContainerPath: "/a/b"},
		{ContainerPath: "/a/b/c/d"},
		{ContainerPath: "/a"},
		{ContainerPath: "/b"},
		{ContainerPath: "/b/c"},
	}
	expected := []*runtime.Mount{
		{ContainerPath: "/a"},
		{ContainerPath: "/b"},
		{ContainerPath: "/a/b"},
		{ContainerPath: "/b/c"},
		{ContainerPath: "/a/b/c"},
		{ContainerPath: "/a/b/c/d"},
	}
	sort.Stable(orderedMounts(mounts))
	assert.Equal(t, expected, mounts)
}
