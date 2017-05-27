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
	"fmt"
	"io"
	"os"
	"syscall"
	"testing"

	"github.com/containerd/containerd/reference"
	imagedigest "github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/assert"
	"golang.org/x/net/context"

	"github.com/kubernetes-incubator/cri-containerd/pkg/metadata"
	ostesting "github.com/kubernetes-incubator/cri-containerd/pkg/os/testing"
)

func TestPrepareStreamingPipes(t *testing.T) {
	for desc, test := range map[string]struct {
		stdin  string
		stdout string
		stderr string
	}{
		"empty stdin": {
			stdout: "/test/stdout",
			stderr: "/test/stderr",
		},
		"empty stdout/stderr": {
			stdin: "/test/stdin",
		},
		"non-empty stdio": {
			stdin:  "/test/stdin",
			stdout: "/test/stdout",
			stderr: "/test/stderr",
		},
		"empty stdio": {},
	} {
		t.Logf("TestCase %q", desc)
		c := newTestCRIContainerdService()
		fakeOS := c.os.(*ostesting.FakeOS)
		fakeOS.OpenFifoFn = func(ctx context.Context, fn string, flag int, perm os.FileMode) (io.ReadWriteCloser, error) {
			expectFlag := syscall.O_RDONLY | syscall.O_CREAT | syscall.O_NONBLOCK
			if fn == test.stdin {
				expectFlag = syscall.O_WRONLY | syscall.O_CREAT | syscall.O_NONBLOCK
			}
			assert.Equal(t, expectFlag, flag)
			assert.Equal(t, os.FileMode(0700), perm)
			return nopReadWriteCloser{}, nil
		}
		i, o, e, err := c.prepareStreamingPipes(context.Background(), test.stdin, test.stdout, test.stderr)
		assert.NoError(t, err)
		assert.Equal(t, test.stdin != "", i != nil)
		assert.Equal(t, test.stdout != "", o != nil)
		assert.Equal(t, test.stderr != "", e != nil)
	}
}

type closeTestReadWriteCloser struct {
	CloseFn func() error
	nopReadWriteCloser
}

func (c closeTestReadWriteCloser) Close() error {
	return c.CloseFn()
}

func TestPrepareStreamingPipesError(t *testing.T) {
	stdin, stdout, stderr := "/test/stdin", "/test/stdout", "/test/stderr"
	for desc, inject := range map[string]map[string]error{
		"should cleanup on stdin error":  {stdin: fmt.Errorf("stdin error")},
		"should cleanup on stdout error": {stdout: fmt.Errorf("stdout error")},
		"should cleanup on stderr error": {stderr: fmt.Errorf("stderr error")},
	} {
		t.Logf("TestCase %q", desc)
		c := newTestCRIContainerdService()
		fakeOS := c.os.(*ostesting.FakeOS)
		openFlags := map[string]bool{
			stdin:  false,
			stdout: false,
			stderr: false,
		}
		fakeOS.OpenFifoFn = func(ctx context.Context, fn string, flag int, perm os.FileMode) (io.ReadWriteCloser, error) {
			if inject[fn] != nil {
				return nil, inject[fn]
			}
			openFlags[fn] = !openFlags[fn]
			testCloser := closeTestReadWriteCloser{}
			testCloser.CloseFn = func() error {
				openFlags[fn] = !openFlags[fn]
				return nil
			}
			return testCloser, nil
		}
		i, o, e, err := c.prepareStreamingPipes(context.Background(), stdin, stdout, stderr)
		assert.Error(t, err)
		assert.Nil(t, i)
		assert.Nil(t, o)
		assert.Nil(t, e)
		assert.False(t, openFlags[stdin])
		assert.False(t, openFlags[stdout])
		assert.False(t, openFlags[stderr])
	}
}

func TestGetSandbox(t *testing.T) {
	c := newTestCRIContainerdService()
	testID := "abcdefg"
	testSandbox := metadata.SandboxMetadata{
		ID:   testID,
		Name: "test-name",
	}
	assert.NoError(t, c.sandboxStore.Create(testSandbox))
	assert.NoError(t, c.sandboxIDIndex.Add(testID))

	for desc, test := range map[string]struct {
		id        string
		expected  *metadata.SandboxMetadata
		expectErr bool
	}{
		"full id": {
			id:        testID,
			expected:  &testSandbox,
			expectErr: false,
		},
		"partial id": {
			id:        testID[:3],
			expected:  &testSandbox,
			expectErr: false,
		},
		"non-exist id": {
			id:        "gfedcba",
			expected:  nil,
			expectErr: true,
		},
	} {
		t.Logf("TestCase %q", desc)
		sb, err := c.getSandbox(test.id)
		if test.expectErr {
			assert.Error(t, err)
			assert.True(t, metadata.IsNotExistError(err))
		} else {
			assert.NoError(t, err)
		}
		assert.Equal(t, test.expected, sb)
	}
}

func TestNormalizeImageRef(t *testing.T) {
	for _, test := range []struct {
		input  string
		expect string
	}{
		{ // has nothing
			input:  "busybox",
			expect: "docker.io/library/busybox:latest",
		},
		{ // only has tag
			input:  "busybox:latest",
			expect: "docker.io/library/busybox:latest",
		},
		{ // only has digest
			input:  "busybox@sha256:e6693c20186f837fc393390135d8a598a96a833917917789d63766cab6c59582",
			expect: "docker.io/library/busybox@sha256:e6693c20186f837fc393390135d8a598a96a833917917789d63766cab6c59582",
		},
		{ // only has path
			input:  "library/busybox",
			expect: "docker.io/library/busybox:latest",
		},
		{ // only has hostname
			input:  "docker.io/busybox",
			expect: "docker.io/library/busybox:latest",
		},
		{ // has no tag
			input:  "docker.io/library/busybox",
			expect: "docker.io/library/busybox:latest",
		},
		{ // has no path
			input:  "docker.io/busybox:latest",
			expect: "docker.io/library/busybox:latest",
		},
		{ // has no hostname
			input:  "library/busybox:latest",
			expect: "docker.io/library/busybox:latest",
		},
		{ // full reference
			input:  "docker.io/library/busybox:latest",
			expect: "docker.io/library/busybox:latest",
		},
		{ // gcr reference
			input:  "gcr.io/library/busybox",
			expect: "gcr.io/library/busybox:latest",
		},
		{ // both tag and digest
			input:  "gcr.io/library/busybox:latest@sha256:e6693c20186f837fc393390135d8a598a96a833917917789d63766cab6c59582",
			expect: "gcr.io/library/busybox@sha256:e6693c20186f837fc393390135d8a598a96a833917917789d63766cab6c59582",
		},
	} {
		t.Logf("TestCase %q", test.input)
		normalized, err := normalizeImageRef(test.input)
		assert.NoError(t, err)
		output := normalized.String()
		assert.Equal(t, test.expect, output)
		_, err = reference.Parse(output)
		assert.NoError(t, err, "%q should be containerd supported reference", output)
	}
}

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
	} {
		t.Logf("TestCase %q", desc)
		named, err := normalizeImageRef(test.ref)
		assert.NoError(t, err)
		repoDigest, repoTag := getRepoDigestAndTag(named, digest)
		assert.Equal(t, test.expectedRepoDigest, repoDigest)
		assert.Equal(t, test.expectedRepoTag, repoTag)
	}
}
