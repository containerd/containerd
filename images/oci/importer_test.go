package oci

import (
	"testing"

	"github.com/gotestyourself/gotestyourself/assert"
	is "github.com/gotestyourself/gotestyourself/assert/cmp"
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
		assert.Check(t, is.NilError(err))
		assert.Check(t, is.Equal(test.expect, normalized))
	}
}
