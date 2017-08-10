// Package oci provides basic operations for manipulating OCI images.
package oci

import (
	"encoding/json"
	"errors"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"

	"github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/specs-go"
	spec "github.com/opencontainers/image-spec/specs-go/v1"
)

// Init initializes the img directory as an OCI image.
// i.e. Creates oci-layout, index.json, and blobs.
//
// img directory must not exist before calling this function.
//
// imageLayoutVersion can be an empty string for specifying the default version.
func Init(img, imageLayoutVersion string) error {
	if imageLayoutVersion == "" {
		imageLayoutVersion = spec.ImageLayoutVersion
	}
	if _, err := os.Stat(img); err == nil {
		return os.ErrExist
	}
	// Create the directory
	if err := os.MkdirAll(img, 0755); err != nil {
		return err
	}
	// Create blobs/sha256
	if err := os.MkdirAll(
		filepath.Join(img, "blobs", string(digest.Canonical)),
		0755); err != nil {
		return nil
	}
	// Create oci-layout
	if err := WriteImageLayout(img, spec.ImageLayout{Version: imageLayoutVersion}); err != nil {
		return err
	}
	// Create index.json
	return WriteIndex(img, spec.Index{Versioned: specs.Versioned{SchemaVersion: 2}})
}

func blobPath(img string, d digest.Digest) string {
	return filepath.Join(img, "blobs", d.Algorithm().String(), d.Hex())
}

func indexPath(img string) string {
	return filepath.Join(img, "index.json")
}

// GetBlobReader returns io.ReadCloser for a blob.
func GetBlobReader(img string, d digest.Digest) (io.ReadCloser, error) {
	// we return a reader rather than the full *os.File here so as to prohibit write operations.
	return os.Open(blobPath(img, d))
}

// ReadBlob reads an OCI blob.
func ReadBlob(img string, d digest.Digest) ([]byte, error) {
	r, err := GetBlobReader(img, d)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return ioutil.ReadAll(r)
}

// WriteBlob writes bytes as an OCI blob and returns its digest using the canonical digest algorithm.
// If you need to specify certain algorithm, you can use NewBlobWriter(img string, algo digest.Algorithm).
func WriteBlob(img string, b []byte) (digest.Digest, error) {
	d := digest.FromBytes(b)
	return d, ioutil.WriteFile(blobPath(img, d), b, 0444)
}

// BlobWriter writes an OCI blob and returns a digest when closed.
type BlobWriter interface {
	io.Writer
	io.Closer
	// Digest returns the digest when closed.
	// Digest panics when the writer is not closed.
	Digest() digest.Digest
}

// blobWriter implements BlobWriter.
type blobWriter struct {
	img      string
	digester digest.Digester
	f        *os.File
	closed   bool
}

// NewBlobWriter returns a BlobWriter.
func NewBlobWriter(img string, algo digest.Algorithm) (BlobWriter, error) {
	// use img rather than the default tmp, so as to make sure rename(2) can be applied
	f, err := ioutil.TempFile(img, "tmp.blobwriter")
	if err != nil {
		return nil, err
	}
	return &blobWriter{
		img:      img,
		digester: algo.Digester(),
		f:        f,
	}, nil
}

// Write implements io.Writer.
func (bw *blobWriter) Write(b []byte) (int, error) {
	n, err := bw.f.Write(b)
	if err != nil {
		return n, err
	}
	return bw.digester.Hash().Write(b)
}

// Close implements io.Closer.
func (bw *blobWriter) Close() error {
	oldPath := bw.f.Name()
	if err := bw.f.Close(); err != nil {
		return err
	}
	newPath := blobPath(bw.img, bw.digester.Digest())
	if err := os.MkdirAll(filepath.Dir(newPath), 0755); err != nil {
		return err
	}
	if err := os.Chmod(oldPath, 0444); err != nil {
		return err
	}
	if err := os.Rename(oldPath, newPath); err != nil {
		return err
	}
	bw.closed = true
	return nil
}

// Digest returns the digest when closed.
func (bw *blobWriter) Digest() digest.Digest {
	if !bw.closed {
		panic("blobWriter is unclosed")
	}
	return bw.digester.Digest()
}

// DeleteBlob deletes an OCI blob.
func DeleteBlob(img string, d digest.Digest) error {
	return os.Remove(blobPath(img, d))
}

// ReadImageLayout returns the image layout.
func ReadImageLayout(img string) (spec.ImageLayout, error) {
	b, err := ioutil.ReadFile(filepath.Join(img, spec.ImageLayoutFile))
	if err != nil {
		return spec.ImageLayout{}, err
	}
	var layout spec.ImageLayout
	if err := json.Unmarshal(b, &layout); err != nil {
		return spec.ImageLayout{}, err
	}
	return layout, nil
}

// WriteImageLayout writes the image layout.
func WriteImageLayout(img string, layout spec.ImageLayout) error {
	b, err := json.Marshal(layout)
	if err != nil {
		return err
	}
	return ioutil.WriteFile(filepath.Join(img, spec.ImageLayoutFile), b, 0644)
}

// ReadIndex returns the index.
func ReadIndex(img string) (spec.Index, error) {
	b, err := ioutil.ReadFile(indexPath(img))
	if err != nil {
		return spec.Index{}, err
	}
	var idx spec.Index
	if err := json.Unmarshal(b, &idx); err != nil {
		return spec.Index{}, err
	}
	return idx, nil
}

// WriteIndex writes the index.
func WriteIndex(img string, idx spec.Index) error {
	b, err := json.Marshal(idx)
	if err != nil {
		return err
	}
	return ioutil.WriteFile(indexPath(img), b, 0644)
}

// RemoveManifestDescriptorFromIndex removes the manifest descriptor from the index.
// Returns nil error when the entry not found.
func RemoveManifestDescriptorFromIndex(img string, refName string) error {
	if refName == "" {
		return errors.New("empty refName specified")
	}
	src, err := ReadIndex(img)
	if err != nil {
		return err
	}
	dst := src
	dst.Manifests = nil
	for _, m := range src.Manifests {
		mRefName, ok := m.Annotations[spec.AnnotationRefName]
		if ok && mRefName == refName {
			continue
		}
		dst.Manifests = append(dst.Manifests, m)
	}
	return WriteIndex(img, dst)
}

// PutManifestDescriptorToIndex puts a manifest descriptor to the index.
// If ref name is set and conflicts with the existing descriptors, the old ones are removed.
func PutManifestDescriptorToIndex(img string, desc spec.Descriptor) error {
	refName, ok := desc.Annotations[spec.AnnotationRefName]
	if ok && refName != "" {
		if err := RemoveManifestDescriptorFromIndex(img, refName); err != nil {
			return err
		}
	}
	idx, err := ReadIndex(img)
	if err != nil {
		return err
	}
	idx.Manifests = append(idx.Manifests, desc)
	return WriteIndex(img, idx)
}

// WriteJSONBlob is an utility function that writes x as a JSON blob with the specified media type, and returns the descriptor.
func WriteJSONBlob(img string, x interface{}, mediaType string) (spec.Descriptor, error) {
	b, err := json.Marshal(x)
	if err != nil {
		return spec.Descriptor{}, err
	}
	d, err := WriteBlob(img, b)
	if err != nil {
		return spec.Descriptor{}, err
	}
	return spec.Descriptor{
		MediaType: mediaType,
		Digest:    d,
		Size:      int64(len(b)),
	}, nil
}
