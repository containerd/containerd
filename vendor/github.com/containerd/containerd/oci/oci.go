// Package oci provides basic operations for manipulating OCI images.
// This package can be used even outside of containerd, and contains some
// functions not used in containerd itself.
package oci

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"

	"github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/specs-go"
	spec "github.com/opencontainers/image-spec/specs-go/v1"
)

// BlobWriter writes an OCI blob and returns a digest when committed.
type BlobWriter interface {
	// Close is expected to be called after Commit() when commission is needed.
	io.WriteCloser
	// Digest may return empty digest or panics until committed.
	Digest() digest.Digest
	// Commit commits the blob (but no roll-back is guaranteed on an error).
	// size and expected can be zero-value when unknown.
	Commit(size int64, expected digest.Digest) error
}

// ErrUnexpectedSize can be returned from BlobWriter.Commit()
type ErrUnexpectedSize struct {
	Expected int64
	Actual   int64
}

func (e ErrUnexpectedSize) Error() string {
	if e.Expected > 0 && e.Expected != e.Actual {
		return fmt.Sprintf("unexpected size: %d != %d", e.Expected, e.Actual)
	}
	return fmt.Sprintf("malformed ErrUnexpectedSize(%+v)", e)
}

// ErrUnexpectedDigest can be returned from BlobWriter.Commit()
type ErrUnexpectedDigest struct {
	Expected digest.Digest
	Actual   digest.Digest
}

func (e ErrUnexpectedDigest) Error() string {
	if e.Expected.String() != "" && e.Expected.String() != e.Actual.String() {
		return fmt.Sprintf("unexpected digest: %v != %v", e.Expected, e.Actual)
	}
	return fmt.Sprintf("malformed ErrUnexpectedDigest(%+v)", e)
}

// ImageDriver corresponds to the representation of an image.
// Path uses os.PathSeparator as the separator.
// The methods of ImageDriver should only be called from oci package.
type ImageDriver interface {
	Init() error
	Remove(path string) error
	Reader(path string) (io.ReadCloser, error)
	Writer(path string, perm os.FileMode) (io.WriteCloser, error)
	BlobWriter(algo digest.Algorithm) (BlobWriter, error)
}

type InitOpts struct {
	// imageLayoutVersion can be an empty string for specifying the default version.
	ImageLayoutVersion string
	// skip creating oci-layout
	SkipCreateImageLayout bool
	// skip creating index.json
	SkipCreateIndex bool
}

// Init initializes an OCI image structure.
// Init calls img.Init, creates `oci-layout`(0444), and creates `index.json`(0644).
//
func Init(img ImageDriver, opts InitOpts) error {
	if err := img.Init(); err != nil {
		return err
	}

	// Create oci-layout
	if !opts.SkipCreateImageLayout {
		imageLayoutVersion := opts.ImageLayoutVersion
		if imageLayoutVersion == "" {
			imageLayoutVersion = spec.ImageLayoutVersion
		}
		if err := WriteImageLayout(img, spec.ImageLayout{Version: imageLayoutVersion}); err != nil {
			return err
		}
	}

	// Create index.json
	if !opts.SkipCreateIndex {
		if err := WriteIndex(img, spec.Index{Versioned: specs.Versioned{SchemaVersion: 2}}); err != nil {
			return err
		}
	}
	return nil
}

func blobPath(d digest.Digest) string {
	return filepath.Join("blobs", d.Algorithm().String(), d.Hex())
}

const (
	indexPath = "index.json"
)

// GetBlobReader returns io.ReadCloser for a blob.
func GetBlobReader(img ImageDriver, d digest.Digest) (io.ReadCloser, error) {
	// we return a reader rather than the full *os.File here so as to prohibit write operations.
	return img.Reader(blobPath(d))
}

// ReadBlob reads an OCI blob.
func ReadBlob(img ImageDriver, d digest.Digest) ([]byte, error) {
	r, err := GetBlobReader(img, d)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return ioutil.ReadAll(r)
}

// WriteBlob writes bytes as an OCI blob and returns its digest using the canonical digest algorithm.
// If you need to specify certain algorithm, you can use NewBlobWriter(img string, algo digest.Algorithm).
func WriteBlob(img ImageDriver, b []byte) (digest.Digest, error) {
	w, err := img.BlobWriter(digest.Canonical)
	if err != nil {
		return "", err
	}
	n, err := w.Write(b)
	if err != nil {
		return "", err
	}
	if n < len(b) {
		return "", io.ErrShortWrite
	}
	if err := w.Close(); err != nil {
		return "", err
	}
	return w.Digest(), err
}

// NewBlobWriter returns a BlobWriter.
func NewBlobWriter(img ImageDriver, algo digest.Algorithm) (BlobWriter, error) {
	return img.BlobWriter(algo)
}

// DeleteBlob deletes an OCI blob.
func DeleteBlob(img ImageDriver, d digest.Digest) error {
	return img.Remove(blobPath(d))
}

// ReadImageLayout returns the image layout.
func ReadImageLayout(img ImageDriver) (spec.ImageLayout, error) {
	r, err := img.Reader(spec.ImageLayoutFile)
	if err != nil {
		return spec.ImageLayout{}, err
	}
	b, err := ioutil.ReadAll(r)
	if err != nil {
		return spec.ImageLayout{}, err
	}
	if err := r.Close(); err != nil {
		return spec.ImageLayout{}, err
	}
	var layout spec.ImageLayout
	if err := json.Unmarshal(b, &layout); err != nil {
		return spec.ImageLayout{}, err
	}
	return layout, nil
}

// WriteImageLayout writes the image layout.
func WriteImageLayout(img ImageDriver, layout spec.ImageLayout) error {
	b, err := json.Marshal(layout)
	if err != nil {
		return err
	}
	w, err := img.Writer(spec.ImageLayoutFile, 0444)
	if err != nil {
		return err
	}
	n, err := w.Write(b)
	if err != nil {
		return err
	}
	if n < len(b) {
		return io.ErrShortWrite
	}
	return w.Close()
}

// ReadIndex returns the index.
func ReadIndex(img ImageDriver) (spec.Index, error) {
	r, err := img.Reader(indexPath)
	if err != nil {
		return spec.Index{}, err
	}
	b, err := ioutil.ReadAll(r)
	if err != nil {
		return spec.Index{}, err
	}
	if err := r.Close(); err != nil {
		return spec.Index{}, err
	}
	var idx spec.Index
	if err := json.Unmarshal(b, &idx); err != nil {
		return spec.Index{}, err
	}
	return idx, nil
}

// WriteIndex writes the index.
func WriteIndex(img ImageDriver, idx spec.Index) error {
	b, err := json.Marshal(idx)
	if err != nil {
		return err
	}
	w, err := img.Writer(indexPath, 0644)
	if err != nil {
		return err
	}
	n, err := w.Write(b)
	if err != nil {
		return err
	}
	if n < len(b) {
		return io.ErrShortWrite
	}
	return w.Close()
}

// RemoveManifestDescriptorFromIndex removes the manifest descriptor from the index.
// Returns nil error when the entry not found.
func RemoveManifestDescriptorFromIndex(img ImageDriver, refName string) error {
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
func PutManifestDescriptorToIndex(img ImageDriver, desc spec.Descriptor) error {
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
func WriteJSONBlob(img ImageDriver, x interface{}, mediaType string) (spec.Descriptor, error) {
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
