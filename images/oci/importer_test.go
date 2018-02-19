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
	"testing"

	"github.com/gotestyourself/gotestyourself/assert"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

func TestNormalizeImageRef(t *testing.T) {
	imageBaseName := "foo/bar"
	for _, test := range []struct {
		input  ocispec.Descriptor
		expect string
	}{
		{
			input: ocispec.Descriptor{
				Digest: digest.Digest("sha256:e22e93af8657d43d7f204b93d69604aeacf273f71d2586288cde312808c0ec77"),
			},
			expect: "foo/bar@sha256:e22e93af8657d43d7f204b93d69604aeacf273f71d2586288cde312808c0ec77",
		},
		{
			input: ocispec.Descriptor{
				Digest: digest.Digest("sha256:e22e93af8657d43d7f204b93d69604aeacf273f71d2586288cde312808c0ec77"),
				Annotations: map[string]string{
					ocispec.AnnotationRefName: "latest",
				},
			},
			expect: "foo/bar:latest", // no @digest for simplicity
		},
	} {
		normalized, err := normalizeImageRef(imageBaseName, test.input)
		assert.NilError(t, err)
		assert.Equal(t, test.expect, normalized)
	}
}
