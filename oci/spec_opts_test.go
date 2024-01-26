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

package oci

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/containerd/containerd/v2/containers"
	"github.com/containerd/containerd/v2/content"
	"github.com/containerd/containerd/v2/errdefs"
	"github.com/containerd/containerd/v2/namespaces"
)

type blob []byte

func (b blob) ReadAt(p []byte, off int64) (int, error) {
	if off >= int64(len(b)) {
		return 0, io.EOF
	}
	return copy(p, b[off:]), nil
}

func (b blob) Close() error {
	return nil
}

func (b blob) Size() int64 {
	return int64(len(b))
}

type fakeImage struct {
	config ocispec.Descriptor
	blobs  map[string]blob
}

func newFakeImage(config ocispec.Image) (Image, error) {
	configBlob, err := json.Marshal(config)
	if err != nil {
		return nil, err
	}
	configDescriptor := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageConfig,
		Digest:    digest.NewDigestFromBytes(digest.SHA256, configBlob),
	}

	return fakeImage{
		config: configDescriptor,
		blobs: map[string]blob{
			configDescriptor.Digest.String(): configBlob,
		},
	}, nil
}

func (i fakeImage) Config(ctx context.Context) (ocispec.Descriptor, error) {
	return i.config, nil
}

func (i fakeImage) ContentStore() content.Store {
	return i
}

func (i fakeImage) ReaderAt(ctx context.Context, dec ocispec.Descriptor) (content.ReaderAt, error) {
	blob, found := i.blobs[dec.Digest.String()]
	if !found {
		return nil, errdefs.ErrNotFound
	}
	return blob, nil
}

func (i fakeImage) Info(ctx context.Context, dgst digest.Digest) (content.Info, error) {
	return content.Info{}, errdefs.ErrNotImplemented
}

func (i fakeImage) Update(ctx context.Context, info content.Info, fieldpaths ...string) (content.Info, error) {
	return content.Info{}, errdefs.ErrNotImplemented
}

func (i fakeImage) Walk(ctx context.Context, fn content.WalkFunc, filters ...string) error {
	return errdefs.ErrNotImplemented
}

func (i fakeImage) Delete(ctx context.Context, dgst digest.Digest) error {
	return errdefs.ErrNotImplemented
}

func (i fakeImage) Status(ctx context.Context, ref string) (content.Status, error) {
	return content.Status{}, errdefs.ErrNotImplemented
}

func (i fakeImage) ListStatuses(ctx context.Context, filters ...string) ([]content.Status, error) {
	return nil, errdefs.ErrNotImplemented
}

func (i fakeImage) Abort(ctx context.Context, ref string) error {
	return errdefs.ErrNotImplemented
}

func (i fakeImage) Writer(ctx context.Context, opts ...content.WriterOpt) (content.Writer, error) {
	return nil, errdefs.ErrNotImplemented
}

func TestReplaceOrAppendEnvValues(t *testing.T) {
	t.Parallel()

	defaults := []string{
		"o=ups", "p=$e", "x=foo", "y=boo", "z", "t=",
	}
	overrides := []string{
		"x=bar", "y", "a=42", "o=", "e", "s=",
	}
	expected := []string{
		"o=", "p=$e", "x=bar", "z", "t=", "a=42", "s=",
	}

	defaultsOrig := make([]string, len(defaults))
	copy(defaultsOrig, defaults)
	overridesOrig := make([]string, len(overrides))
	copy(overridesOrig, overrides)

	results := replaceOrAppendEnvValues(defaults, overrides)

	if err := assertEqualsStringArrays(defaults, defaultsOrig); err != nil {
		t.Fatal(err)
	}
	if err := assertEqualsStringArrays(overrides, overridesOrig); err != nil {
		t.Fatal(err)
	}

	if err := assertEqualsStringArrays(results, expected); err != nil {
		t.Fatal(err)
	}
}

func TestWithDefaultSpecForPlatform(t *testing.T) {
	t.Parallel()
	var (
		s   Spec
		c   = containers.Container{ID: "TestWithDefaultSpecForPlatform"}
		ctx = namespaces.WithNamespace(context.Background(), "test")
	)

	platforms := []string{"linux/amd64", "windows/amd64"}
	for _, p := range platforms {
		if err := ApplyOpts(ctx, nil, &c, &s, WithDefaultSpecForPlatform(p)); err != nil {
			t.Fatal(err)
		}
	}

}

func Contains(a []string, x string) bool {
	for _, n := range a {
		if x == n {
			return true
		}
	}
	return false
}

func TestWithProcessCwd(t *testing.T) {
	t.Parallel()
	s := Spec{}
	opts := []SpecOpts{
		WithProcessCwd("testCwd"),
	}
	var expectedCwd = "testCwd"

	for _, opt := range opts {
		if err := opt(nil, nil, nil, &s); err != nil {
			t.Fatal(err)
		}
	}
	if s.Process.Cwd != expectedCwd {
		t.Fatal("Process has a wrong current working directory")
	}

}

func TestWithEnv(t *testing.T) {
	t.Parallel()

	s := Spec{}
	s.Process = &specs.Process{
		Env: []string{"DEFAULT=test"},
	}

	WithEnv([]string{"env=1"})(context.Background(), nil, nil, &s)

	if len(s.Process.Env) != 2 {
		t.Fatal("didn't append")
	}

	WithEnv([]string{"env2=1"})(context.Background(), nil, nil, &s)

	if len(s.Process.Env) != 3 {
		t.Fatal("didn't append")
	}

	WithEnv([]string{"env2=2"})(context.Background(), nil, nil, &s)

	if s.Process.Env[2] != "env2=2" {
		t.Fatal("couldn't update")
	}

	WithEnv([]string{"env2"})(context.Background(), nil, nil, &s)

	if len(s.Process.Env) != 2 {
		t.Fatal("couldn't unset")
	}
}

func TestWithMounts(t *testing.T) {

	t.Parallel()

	s := Spec{
		Mounts: []specs.Mount{
			{
				Source:      "default-source",
				Destination: "default-dest",
			},
		},
	}

	WithMounts([]specs.Mount{
		{
			Source:      "new-source",
			Destination: "new-dest",
		},
	})(nil, nil, nil, &s)

	if len(s.Mounts) != 2 {
		t.Fatal("didn't append")
	}

	if s.Mounts[1].Source != "new-source" {
		t.Fatal("invalid mount")
	}

	if s.Mounts[1].Destination != "new-dest" {
		t.Fatal("invalid mount")
	}
}

func TestWithDefaultSpec(t *testing.T) {
	t.Parallel()
	var (
		s   Spec
		c   = containers.Container{ID: "TestWithDefaultSpec"}
		ctx = namespaces.WithNamespace(context.Background(), "test")
	)

	if err := ApplyOpts(ctx, nil, &c, &s, WithDefaultSpec()); err != nil {
		t.Fatal(err)
	}

	var (
		expected Spec
		err      error
	)

	switch runtime.GOOS {
	case "windows":
		err = populateDefaultWindowsSpec(ctx, &expected, c.ID)
	case "darwin":
		err = populateDefaultDarwinSpec(&expected)
	default:
		err = populateDefaultUnixSpec(ctx, &expected, c.ID)
	}

	if err != nil {
		t.Fatal(err)
	}

	if reflect.DeepEqual(s, Spec{}) {
		t.Fatalf("spec should not be empty")
	}

	if !reflect.DeepEqual(&s, &expected) {
		t.Fatalf("spec from option differs from default: \n%#v != \n%#v", &s, expected)
	}
}

func TestWithSpecFromFile(t *testing.T) {
	t.Parallel()
	var (
		s   Spec
		c   = containers.Container{ID: "TestWithDefaultSpec"}
		ctx = namespaces.WithNamespace(context.Background(), "test")
	)

	fp, err := os.CreateTemp("", "testwithdefaultspec.json")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.Remove(fp.Name()); err != nil {
			log.Printf("failed to remove tempfile %v: %v", fp.Name(), err)
		}
	}()
	defer fp.Close()

	expected, err := GenerateSpec(ctx, nil, &c)
	if err != nil {
		t.Fatal(err)
	}

	p, err := json.Marshal(expected)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := fp.Write(p); err != nil {
		t.Fatal(err)
	}

	if err := ApplyOpts(ctx, nil, &c, &s, WithSpecFromFile(fp.Name())); err != nil {
		t.Fatal(err)
	}

	if reflect.DeepEqual(s, Spec{}) {
		t.Fatalf("spec should not be empty")
	}

	if !reflect.DeepEqual(&s, expected) {
		t.Fatalf("spec from option differs from default: \n%#v != \n%#v", &s, expected)
	}
}

func TestWithMemoryLimit(t *testing.T) {
	var (
		ctx = namespaces.WithNamespace(context.Background(), "testing")
		c   = containers.Container{ID: t.Name()}
		m   = uint64(768 * 1024 * 1024)
		o   = WithMemoryLimit(m)
	)
	// Test with all three supported scenarios
	platforms := []string{"", "linux/amd64", "windows/amd64"}
	for _, p := range platforms {
		var spec *Spec
		var err error
		if p == "" {
			t.Log("Testing GenerateSpec default platform")
			spec, err = GenerateSpec(ctx, nil, &c, o)

			// Convert the platform to the default based on GOOS like
			// GenerateSpec does.
			switch runtime.GOOS {
			case "linux":
				p = "linux/amd64"
			case "windows":
				p = "windows/amd64"
			}
		} else {
			t.Logf("Testing GenerateSpecWithPlatform with platform: '%s'", p)
			spec, err = GenerateSpecWithPlatform(ctx, nil, p, &c, o)
		}
		if err != nil {
			t.Fatalf("failed to generate spec with: %v", err)
		}
		switch p {
		case "linux/amd64":
			if *spec.Linux.Resources.Memory.Limit != int64(m) {
				t.Fatalf("spec.Linux.Resources.Memory.Limit expected: %v, got: %v", m, *spec.Linux.Resources.Memory.Limit)
			}
			// If we are linux/amd64 on Windows GOOS it is LCOW
			if runtime.GOOS == "windows" {
				// Verify that we also set the Windows section.
				if *spec.Windows.Resources.Memory.Limit != m {
					t.Fatalf("for LCOW spec.Windows.Resources.Memory.Limit is also expected: %v, got: %v", m, *spec.Windows.Resources.Memory.Limit)
				}
			} else {
				if spec.Windows != nil {
					t.Fatalf("spec.Windows section should not be set for linux/amd64 spec on non-windows platform")
				}
			}
		case "windows/amd64":
			if *spec.Windows.Resources.Memory.Limit != m {
				t.Fatalf("spec.Windows.Resources.Memory.Limit expected: %v, got: %v", m, *spec.Windows.Resources.Memory.Limit)
			}
			if spec.Linux != nil {
				t.Fatalf("spec.Linux section should not be set for windows/amd64 spec ever")
			}
		}
	}
}

func isEqualStringArrays(values, expected []string) bool {
	if len(values) != len(expected) {
		return false
	}

	for i, x := range expected {
		if values[i] != x {
			return false
		}
	}
	return true
}

func assertEqualsStringArrays(values, expected []string) error {
	if !isEqualStringArrays(values, expected) {
		return fmt.Errorf("expected %s, but found %s", expected, values)
	}
	return nil
}

func TestWithTTYSize(t *testing.T) {
	t.Parallel()
	s := Spec{}
	opts := []SpecOpts{
		WithTTYSize(10, 20),
	}
	var (
		expectedWidth  = uint(10)
		expectedHeight = uint(20)
	)

	for _, opt := range opts {
		if err := opt(nil, nil, nil, &s); err != nil {
			t.Fatal(err)
		}
	}
	if s.Process.ConsoleSize.Height != expectedWidth && s.Process.ConsoleSize.Height != expectedHeight {
		t.Fatal("Process Console has invalid size")
	}

}

func TestWithUserNamespace(t *testing.T) {
	t.Parallel()
	s := Spec{}

	opts := []SpecOpts{
		WithUserNamespace([]specs.LinuxIDMapping{
			{
				ContainerID: 1,
				HostID:      2,
				Size:        10000,
			},
		}, []specs.LinuxIDMapping{
			{
				ContainerID: 2,
				HostID:      3,
				Size:        20000,
			},
		}),
	}

	for _, opt := range opts {
		if err := opt(nil, nil, nil, &s); err != nil {
			t.Fatal(err)
		}
	}

	expectedUIDMapping := specs.LinuxIDMapping{
		ContainerID: 1,
		HostID:      2,
		Size:        10000,
	}
	expectedGIDMapping := specs.LinuxIDMapping{
		ContainerID: 2,
		HostID:      3,
		Size:        20000,
	}

	if !(len(s.Linux.UIDMappings) == 1 && s.Linux.UIDMappings[0] == expectedUIDMapping) || !(len(s.Linux.GIDMappings) == 1 && s.Linux.GIDMappings[0] == expectedGIDMapping) {
		t.Fatal("WithUserNamespace Cannot set the uid/gid mappings for the task")
	}

}
func TestWithImageConfigArgs(t *testing.T) {
	t.Parallel()

	img, err := newFakeImage(ocispec.Image{
		Config: ocispec.ImageConfig{
			Env:        []string{"z=bar", "y=baz"},
			Entrypoint: []string{"create", "--namespace=test"},
			Cmd:        []string{"", "--debug"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	s := Spec{
		Version: specs.Version,
		Root:    &specs.Root{},
		Windows: &specs.Windows{},
	}

	opts := []SpecOpts{
		WithEnv([]string{"x=foo", "y=boo"}),
		WithProcessArgs("run", "--foo", "xyz", "--bar"),
		WithImageConfigArgs(img, []string{"--boo", "bar"}),
	}

	expectedEnv := []string{"z=bar", "y=boo", "x=foo"}
	expectedArgs := []string{"create", "--namespace=test", "--boo", "bar"}

	for _, opt := range opts {
		if err := opt(nil, nil, nil, &s); err != nil {
			t.Fatal(err)
		}
	}

	if err := assertEqualsStringArrays(s.Process.Env, expectedEnv); err != nil {
		t.Fatal(err)
	}
	if err := assertEqualsStringArrays(s.Process.Args, expectedArgs); err != nil {
		t.Fatal(err)
	}
}

func TestDevShmSize(t *testing.T) {
	t.Parallel()

	ss := []Spec{
		{
			Mounts: []specs.Mount{
				{
					Destination: "/dev/shm",
					Type:        "tmpfs",
					Source:      "shm",
					Options:     []string{"nosuid", "noexec", "nodev", "mode=1777"},
				},
			},
		},
		{
			Mounts: []specs.Mount{
				{
					Destination: "/test/shm",
					Type:        "tmpfs",
					Source:      "shm",
					Options:     []string{"nosuid", "noexec", "nodev", "mode=1777", "size=65536k"},
				},
			},
		},
		{
			Mounts: []specs.Mount{
				{
					Destination: "/test/shm",
					Type:        "tmpfs",
					Source:      "shm",
					Options:     []string{"nosuid", "noexec", "nodev", "mode=1777", "size=65536k"},
				},
				{
					Destination: "/dev/shm",
					Type:        "tmpfs",
					Source:      "shm",
					Options:     []string{"nosuid", "noexec", "nodev", "mode=1777", "size=65536k", "size=131072k"},
				},
			},
		},
	}

	expected := "1024k"
	for _, s := range ss {
		s := s
		if err := WithDevShmSize(1024)(nil, nil, nil, &s); err != nil {
			if err != ErrNoShmMount {
				t.Fatal(err)
			}

			if getDevShmMount(&s) == nil {
				continue
			}
			t.Fatal("excepted nil /dev/shm mount")
		}

		m := getDevShmMount(&s)
		if m == nil {
			t.Fatal("no shm mount found")
		}
		size, err := getShmSize(m.Options)
		if err != nil {
			t.Fatal(err)
		}
		if size != expected {
			t.Fatalf("size %s not equal %s", size, expected)
		}
	}
}

func getDevShmMount(s *Spec) *specs.Mount {
	for _, m := range s.Mounts {
		if filepath.Clean(m.Destination) == "/dev/shm" && m.Source == "shm" && m.Type == "tmpfs" {
			return &m
		}
	}
	return nil
}

func getShmSize(opts []string) (string, error) {
	// linux will use the last size option
	var so string
	for _, o := range opts {
		if strings.HasPrefix(o, "size=") {
			if so != "" {
				return "", errors.New("contains multiple size options")
			}
			so = o
		}
	}
	if so == "" {
		return "", errors.New("shm size not specified")
	}

	parts := strings.Split(so, "=")
	if len(parts) != 2 {
		return "", errors.New("invalid size format")
	}
	return parts[1], nil
}

func TestWithoutMounts(t *testing.T) {
	t.Parallel()
	var s Spec

	x := func(s string) string {
		if runtime.GOOS == "windows" {
			return filepath.Join("C:\\", filepath.Clean(s))
		}
		return s
	}
	opts := []SpecOpts{
		WithMounts([]specs.Mount{
			{
				Destination: x("/dst1"),
				Source:      x("/src1"),
			},
			{
				Destination: x("/dst2"),
				Source:      x("/src2"),
			},
			{
				Destination: x("/dst3"),
				Source:      x("/src3"),
			},
		}),
		WithoutMounts(x("/dst2"), x("/dst3")),
		WithMounts([]specs.Mount{
			{
				Destination: x("/dst4"),
				Source:      x("/src4"),
			},
		}),
	}

	expected := []specs.Mount{
		{
			Destination: x("/dst1"),
			Source:      x("/src1"),
		},
		{
			Destination: x("/dst4"),
			Source:      x("/src4"),
		},
	}

	for _, opt := range opts {
		if err := opt(nil, nil, nil, &s); err != nil {
			t.Fatal(err)
		}
	}

	if !reflect.DeepEqual(expected, s.Mounts) {
		t.Fatalf("expected %+v, got %+v", expected, s.Mounts)
	}
}

func TestWithWindowsDevice(t *testing.T) {
	testcases := []struct {
		name   string
		idType string
		id     string

		expectError            bool
		expectedWindowsDevices []specs.WindowsDevice
	}{
		{
			name:        "empty_idType_and_id",
			idType:      "",
			id:          "",
			expectError: true,
		},
		{
			name:        "empty_idType",
			idType:      "",
			id:          "5B45201D-F2F2-4F3B-85BB-30FF1F953599",
			expectError: true,
		},
		{
			name:   "empty_id",
			idType: "class",
			id:     "",

			expectError:            false,
			expectedWindowsDevices: []specs.WindowsDevice{{ID: "", IDType: "class"}},
		},
		{
			name:   "idType_and_id",
			idType: "class",
			id:     "5B45201D-F2F2-4F3B-85BB-30FF1F953599",

			expectError:            false,
			expectedWindowsDevices: []specs.WindowsDevice{{ID: "5B45201D-F2F2-4F3B-85BB-30FF1F953599", IDType: "class"}},
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			spec := Spec{
				Version: specs.Version,
				Root:    &specs.Root{},
				Windows: &specs.Windows{},
			}

			opts := []SpecOpts{
				WithWindowsDevice(tc.idType, tc.id),
			}

			for _, opt := range opts {
				if err := opt(nil, nil, nil, &spec); err != nil {
					if tc.expectError {
						assert.Error(t, err)
					} else {
						require.NoError(t, err)
					}
				}
			}

			if len(tc.expectedWindowsDevices) != 0 {
				require.NotNil(t, spec.Windows)
				require.NotNil(t, spec.Windows.Devices)
				assert.ElementsMatch(t, spec.Windows.Devices, tc.expectedWindowsDevices)
			} else if spec.Windows != nil && spec.Windows.Devices != nil {
				assert.ElementsMatch(t, spec.Windows.Devices, tc.expectedWindowsDevices)
			}
		})
	}
}
