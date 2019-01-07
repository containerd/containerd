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
	"io/ioutil"
	"log"
	"os"
	"reflect"
	"runtime"
	"testing"

	"github.com/containerd/containerd/content"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/containerd/containerd/containers"
	"github.com/containerd/containerd/namespaces"
	specs "github.com/opencontainers/runtime-spec/specs-go"
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
		return nil, errors.New("not found")
	}
	return blob, nil
}

func (i fakeImage) Info(ctx context.Context, dgst digest.Digest) (content.Info, error) {
	return content.Info{}, errors.New("not implemented")
}

func (i fakeImage) Update(ctx context.Context, info content.Info, fieldpaths ...string) (content.Info, error) {
	return content.Info{}, errors.New("not implemented")
}

func (i fakeImage) Walk(ctx context.Context, fn content.WalkFunc, filters ...string) error {
	return errors.New("not implemented")
}

func (i fakeImage) Delete(ctx context.Context, dgst digest.Digest) error {
	return errors.New("not implemented")
}

func (i fakeImage) Status(ctx context.Context, ref string) (content.Status, error) {
	return content.Status{}, errors.New("not implemented")
}

func (i fakeImage) ListStatuses(ctx context.Context, filters ...string) ([]content.Status, error) {
	return nil, errors.New("not implemented")
}

func (i fakeImage) Abort(ctx context.Context, ref string) error {
	return errors.New("not implemented")
}

func (i fakeImage) Writer(ctx context.Context, opts ...content.WriterOpt) (content.Writer, error) {
	return nil, errors.New("not implemented")
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

func TestWithEnv(t *testing.T) {
	t.Parallel()

	s := Spec{}
	s.Process = &specs.Process{
		Env: []string{"DEFAULT=test"},
	}

	WithEnv([]string{"env=1"})(nil, nil, nil, &s)

	if len(s.Process.Env) != 2 {
		t.Fatal("didn't append")
	}

	WithEnv([]string{"env2=1"})(nil, nil, nil, &s)

	if len(s.Process.Env) != 3 {
		t.Fatal("didn't append")
	}

	WithEnv([]string{"env2=2"})(nil, nil, nil, &s)

	if s.Process.Env[2] != "env2=2" {
		t.Fatal("couldn't update")
	}

	WithEnv([]string{"env2"})(nil, nil, nil, &s)

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

	var expected Spec
	var err error
	if runtime.GOOS == "windows" {
		err = populateDefaultWindowsSpec(ctx, &expected, c.ID)
	} else {
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

	fp, err := ioutil.TempFile("", "testwithdefaultspec.json")
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

	expectedEnv := []string{"x=foo", "y=baz", "z=bar"}
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
