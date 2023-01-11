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

package image

import (
	"context"
	"errors"
	"testing"

	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/images"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

func TestStore(t *testing.T) {
	for _, testCase := range []struct {
		Name       string
		ImageStore *Store
		// Annotations are the different references annotations to run the test with,
		// the possible values:
		// - "OCI": Uses the OCI defined annotation "org.opencontainers.image.ref.name"
		//   This annotation may be a full reference or tag only
		// - "containerd": Uses the containerd defined annotation "io.containerd.image.name"
		//   This annotation is always a full reference as used by containerd
		// - "Annotation": Sets the annotation flag but does not set a reference annotation
		//   Use this case to test the default where no reference is provided
		// - "NoAnnotation": Does not set the annotation flag
		//   Use this case to test storing of the index images by reference
		Annotations []string
		ImageName   string
		Images      []string
		Err         error
	}{
		{
			Name: "Prefix",
			ImageStore: &Store{
				extraReferences: []Reference{
					{
						Name:     "registry.test/image",
						IsPrefix: true,
					},
				},
			},
			Annotations: []string{"OCI", "containerd"},
			ImageName:   "registry.test/image:latest",
			Images:      []string{"registry.test/image:latest"},
		},
		{
			Name: "Overwrite",
			ImageStore: &Store{
				extraReferences: []Reference{
					{
						Name:           "placeholder",
						IsPrefix:       true,
						AllowOverwrite: true,
					},
				},
			},
			Annotations: []string{"OCI", "containerd"},
			ImageName:   "registry.test/image:latest",
			Images:      []string{"registry.test/image:latest"},
		},
		{
			Name: "TagOnly",
			ImageStore: &Store{
				extraReferences: []Reference{
					{
						Name:     "registry.test/image",
						IsPrefix: true,
					},
				},
			},
			Annotations: []string{"OCI"},
			ImageName:   "latest",
			Images:      []string{"registry.test/image:latest"},
		},
		{
			Name: "AddDigest",
			ImageStore: &Store{
				extraReferences: []Reference{
					{
						Name:      "registry.test/base",
						IsPrefix:  true,
						AddDigest: true,
					},
				},
			},
			Annotations: []string{"Annotation"},
			Images:      []string{"registry.test/base@"},
		},
		{
			Name: "NameAndDigest",
			ImageStore: &Store{
				extraReferences: []Reference{
					{
						Name:      "registry.test/base",
						IsPrefix:  true,
						AddDigest: true,
					},
				},
			},
			Annotations: []string{"OCI"},
			ImageName:   "latest",
			Images:      []string{"registry.test/base:latest", "registry.test/base@"},
		},
		{
			Name: "NameSkipDigest",
			ImageStore: &Store{
				extraReferences: []Reference{
					{
						Name:            "registry.test/base",
						IsPrefix:        true,
						AddDigest:       true,
						SkipNamedDigest: true,
					},
				},
			},
			Annotations: []string{"OCI"},
			ImageName:   "latest",
			Images:      []string{"registry.test/base:latest"},
		},
		{
			Name: "OverwriteNameDigest",
			ImageStore: &Store{
				extraReferences: []Reference{
					{
						Name:           "base-name",
						IsPrefix:       true,
						AllowOverwrite: true,
						AddDigest:      true,
					},
				},
			},
			Annotations: []string{"OCI", "containerd"},
			ImageName:   "registry.test/base:latest",
			Images:      []string{"registry.test/base:latest", "base-name@"},
		},
		{
			Name: "OverwriteNameSkipDigest",
			ImageStore: &Store{
				extraReferences: []Reference{
					{
						Name:            "base-name",
						IsPrefix:        true,
						AllowOverwrite:  true,
						AddDigest:       true,
						SkipNamedDigest: true,
					},
				},
			},
			Annotations: []string{"OCI", "containerd"},
			ImageName:   "registry.test/base:latest",
			Images:      []string{"registry.test/base:latest"},
		},
		{
			Name: "ReferenceNotFound",
			ImageStore: &Store{
				extraReferences: []Reference{
					{
						Name:     "registry.test/image",
						IsPrefix: true,
					},
				},
			},
			Annotations: []string{"OCI", "containerd"},
			ImageName:   "registry.test/base:latest",
			Err:         errdefs.ErrNotFound,
		},
		{
			Name:        "NoReference",
			ImageStore:  &Store{},
			Annotations: []string{"Annotation", "NoAnnotation"},
			Err:         errdefs.ErrNotFound,
		},
		{
			Name: "ImageName",
			ImageStore: &Store{
				imageName: "registry.test/index:latest",
			},
			Annotations: []string{"NoAnnotation"},
			Images:      []string{"registry.test/index:latest"},
		},
	} {
		testCase := testCase
		for _, a := range testCase.Annotations {
			name := testCase.Name + "_" + a
			dgst := digest.Canonical.FromString(name)
			desc := ocispec.Descriptor{
				Digest:      dgst,
				Annotations: map[string]string{},
			}
			expected := make([]string, len(testCase.Images))
			for i, img := range testCase.Images {
				if img[len(img)-1] == '@' {
					img = img + dgst.String()
				}
				expected[i] = img
			}
			switch a {
			case "containerd":
				desc.Annotations["io.containerd.import.ref-source"] = "annotation"
				desc.Annotations[images.AnnotationImageName] = testCase.ImageName
			case "OCI":
				desc.Annotations["io.containerd.import.ref-source"] = "annotation"
				desc.Annotations[ocispec.AnnotationRefName] = testCase.ImageName
			case "Annotation":
				desc.Annotations["io.containerd.import.ref-source"] = "annotation"
			}
			t.Run(name, func(t *testing.T) {
				imgs, err := testCase.ImageStore.Store(context.Background(), desc, nopImageStore{})
				if err != nil {
					if testCase.Err == nil {
						t.Fatal(err)
					}
					if !errors.Is(err, testCase.Err) {
						t.Fatalf("unexpected error %v: expeceted %v", err, testCase.Err)
					}
					return
				} else if testCase.Err != nil {
					t.Fatalf("succeeded but expected error: %v", testCase.Err)
				}

				if len(imgs) != len(expected) {
					t.Fatalf("mismatched array length\nexpected:\n\t%v\nactual\n\t%v", expected, imgs)
				}
				for i, name := range expected {
					if imgs[i].Name != name {
						t.Fatalf("wrong image name %q, expected %q", imgs[i].Name, name)
					}
					if imgs[i].Target.Digest != dgst {
						t.Fatalf("wrong image digest %s, expected %s", imgs[i].Target.Digest, dgst)
					}
				}
			})
		}

	}
}

type nopImageStore struct{}

func (nopImageStore) Get(ctx context.Context, name string) (images.Image, error) {
	return images.Image{}, errdefs.ErrNotFound
}

func (nopImageStore) List(ctx context.Context, filters ...string) ([]images.Image, error) {
	return nil, nil
}

func (nopImageStore) Create(ctx context.Context, image images.Image) (images.Image, error) {
	return image, nil
}

func (nopImageStore) Update(ctx context.Context, image images.Image, fieldpaths ...string) (images.Image, error) {
	return image, nil
}

func (nopImageStore) Delete(ctx context.Context, name string, opts ...images.DeleteOpt) error {
	return nil
}
