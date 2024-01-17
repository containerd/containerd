//go:build !windows

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
	"testing"

	"github.com/containerd/containerd/v2/core/containers"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	specs "github.com/opencontainers/runtime-spec/specs-go"
)

func TestWithImageConfigNoEnv(t *testing.T) {
	t.Parallel()
	var (
		s   Spec
		c   = containers.Container{ID: t.Name()}
		ctx = namespaces.WithNamespace(context.Background(), "test")
	)

	err := populateDefaultUnixSpec(ctx, &s, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	// test hack: we don't want to test the WithAdditionalGIDs portion of the image config code
	s.Windows = &specs.Windows{}

	img, err := newFakeImage(ocispec.Image{
		Config: ocispec.ImageConfig{
			Entrypoint: []string{"create", "--namespace=test"},
			Cmd:        []string{"", "--debug"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	opts := []SpecOpts{
		WithImageConfigArgs(img, []string{"--boo", "bar"}),
	}

	// verify that if an image has no environment that we get a default Unix path
	expectedEnv := []string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"}

	for _, opt := range opts {
		if err := opt(nil, nil, nil, &s); err != nil {
			t.Fatal(err)
		}
	}

	if err := assertEqualsStringArrays(s.Process.Env, expectedEnv); err != nil {
		t.Fatal(err)
	}
}
