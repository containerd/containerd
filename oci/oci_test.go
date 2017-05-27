package oci

import (
	"encoding/json"
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"

	"github.com/containerd/containerd/fs/fstest"
	"github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/specs-go"
	spec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
)

func TestInitError(t *testing.T) {
	tmp, err := ioutil.TempDir("", "oci")
	assert.Nil(t, err)
	defer os.RemoveAll(tmp)
	err = Init(tmp, "")
	assert.Error(t, err, "file exists")
}

func TestInit(t *testing.T) {
	tmp, err := ioutil.TempDir("", "oci")
	assert.Nil(t, err)
	defer os.RemoveAll(tmp)
	img := filepath.Join(tmp, "foo")
	err = Init(img, "")
	assert.Nil(t, err)
	ociLayout, err := json.Marshal(spec.ImageLayout{Version: spec.ImageLayoutVersion})
	assert.Nil(t, err)
	indexJSON, err := json.Marshal(spec.Index{Versioned: specs.Versioned{SchemaVersion: 2}})
	applier := fstest.Apply(
		fstest.CreateDir("/foo", 0755),
		fstest.CreateDir("/foo/blobs", 0755),
		fstest.CreateDir("/foo/blobs/"+string(digest.Canonical), 0755),
		fstest.CreateFile("/foo/oci-layout", ociLayout, 0644),
		fstest.CreateFile("/foo/index.json", indexJSON, 0644),
	)
	err = fstest.CheckDirectoryEqualWithApplier(tmp, applier)
	assert.Nil(t, err)
}

func TestWriteReadDeleteBlob(t *testing.T) {
	tmp, err := ioutil.TempDir("", "oci")
	assert.Nil(t, err)
	defer os.RemoveAll(tmp)
	img := filepath.Join(tmp, "foo")
	err = Init(img, "")
	assert.Nil(t, err)
	testBlob := []byte("test")
	// Write
	d, err := WriteBlob(img, testBlob)
	applier := fstest.Apply(
		fstest.CreateFile("/"+d.Hex(), testBlob, 0444),
	)
	err = fstest.CheckDirectoryEqualWithApplier(filepath.Join(img, "blobs", string(digest.Canonical)), applier)
	assert.Nil(t, err)
	// Read
	b, err := ReadBlob(img, d)
	assert.Nil(t, err)
	assert.Equal(t, testBlob, b)
	// Delete
	err = DeleteBlob(img, d)
	assert.Nil(t, err)
	applier = fstest.Apply()
	err = fstest.CheckDirectoryEqualWithApplier(filepath.Join(img, "blobs", string(digest.Canonical)), applier)
	assert.Nil(t, err)
}

func TestBlobWriter(t *testing.T) {
	tmp, err := ioutil.TempDir("", "oci")
	assert.Nil(t, err)
	defer os.RemoveAll(tmp)
	img := filepath.Join(tmp, "foo")
	err = Init(img, "")
	assert.Nil(t, err)
	testBlob := []byte("test")
	w, err := NewBlobWriter(img, digest.Canonical)
	_, err = w.Write(testBlob)
	assert.Nil(t, err)
	// blob is not written until closing
	applier := fstest.Apply()
	err = fstest.CheckDirectoryEqualWithApplier(filepath.Join(img, "blobs", string(digest.Canonical)), applier)
	// digest is unavailable until closing
	assert.Panics(t, func() { w.Digest() })
	// close and calculate the digest
	err = w.Close()
	assert.Nil(t, err)
	d := w.Digest()
	applier = fstest.Apply(
		fstest.CreateFile("/"+d.Hex(), testBlob, 0444),
	)
	err = fstest.CheckDirectoryEqualWithApplier(filepath.Join(img, "blobs", string(digest.Canonical)), applier)
	assert.Nil(t, err)
}

func TestIndex(t *testing.T) {
	tmp, err := ioutil.TempDir("", "oci")
	assert.Nil(t, err)
	defer os.RemoveAll(tmp)
	img := filepath.Join(tmp, "foo")
	err = Init(img, "")
	assert.Nil(t, err)
	descs := []spec.Descriptor{
		{
			MediaType: spec.MediaTypeImageManifest,
			Annotations: map[string]string{
				spec.AnnotationRefName: "foo",
				"dummy":                "desc0",
			},
		},
		{
			MediaType: spec.MediaTypeImageManifest,
			Annotations: map[string]string{
				// will be removed later
				spec.AnnotationRefName: "bar",
				"dummy":                "desc1",
			},
		},
		{
			MediaType: spec.MediaTypeImageManifest,
			Annotations: map[string]string{
				// duplicated ref name
				spec.AnnotationRefName: "foo",
				"dummy":                "desc2",
			},
		},
		{
			MediaType: spec.MediaTypeImageManifest,
			Annotations: map[string]string{
				// no ref name
				"dummy": "desc3",
			},
		},
	}
	for _, desc := range descs {
		err := PutManifestDescriptorToIndex(img, desc)
		assert.Nil(t, err)
	}
	err = RemoveManifestDescriptorFromIndex(img, "bar")
	assert.Nil(t, err)
	expected := spec.Index{
		Versioned: specs.Versioned{SchemaVersion: 2},
		Manifests: []spec.Descriptor{
			{
				MediaType: spec.MediaTypeImageManifest,
				Annotations: map[string]string{
					// duplicated ref name
					spec.AnnotationRefName: "foo",
					"dummy":                "desc2",
				},
			},
			{
				MediaType: spec.MediaTypeImageManifest,
				Annotations: map[string]string{
					// no ref name
					"dummy": "desc3",
				},
			},
		},
	}
	idx, err := ReadIndex(img)
	assert.Nil(t, err)
	assert.Equal(t, expected, idx)
}
