package image

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

const (
	RefNameAnnotation = "org.opencontainers.image.ref.name" // should it be defined in image-spec?
)

func Init(img string) error {
	// Create the directory
	if err := os.RemoveAll(img); err != nil {
		return err
	}
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
	if err := WriteImageLayout(img, &spec.ImageLayout{Version: spec.ImageLayoutVersion}); err != nil {
		return err
	}
	// Create index.json
	return WriteIndex(img, &spec.Index{Versioned: specs.Versioned{SchemaVersion: 2}})
}

func blobPath(img string, d digest.Digest) string {
	return filepath.Join(img, "blobs", d.Algorithm().String(), d.Hex())
}

func indexPath(img string) string {
	return filepath.Join(img, "index.json")
}

type BlobReader interface {
	io.ReadSeeker
	io.Closer
}

func GetBlobReader(img string, d digest.Digest) (BlobReader, error) {
	return os.Open(blobPath(img, d))
}

func ReadBlob(img string, d digest.Digest) ([]byte, error) {
	r, err := GetBlobReader(img, d)
	if err != nil {
		return nil, err
	}
	return ioutil.ReadAll(r)
}

func WriteBlob(img string, b []byte) (digest.Digest, error) {
	d := digest.FromBytes(b)
	return d, ioutil.WriteFile(blobPath(img, d), b, 0444)
}

type BlobWriter struct {
	img      string
	digester digest.Digester
	f        *os.File
	closed   bool
}

func NewBlobWriter(img string, algo digest.Algorithm) (*BlobWriter, error) {
	// use img rather than the default tmp, so as to make sure rename(2) can be applied
	f, err := ioutil.TempFile(img, "tmp.blobwriter")
	if err != nil {
		return nil, err
	}
	return &BlobWriter{
		img:      img,
		digester: algo.Digester(),
		f:        f,
	}, nil
}

func (bw *BlobWriter) Write(b []byte) (int, error) {
	n, err := bw.f.Write(b)
	if err != nil {
		return n, err
	}
	return bw.digester.Hash().Write(b)
}

func (bw *BlobWriter) Close() error {
	oldPath := bw.f.Name()
	if err := bw.f.Close(); err != nil {
		return err
	}
	newPath := blobPath(bw.img, bw.digester.Digest())
	if err := os.MkdirAll(filepath.Dir(newPath), 0755); err != nil {
		return err
	}
	if err := os.Rename(oldPath, newPath); err != nil {
		return err
	}
	bw.closed = true
	return nil
}

// Digest returns nil if unclosed
func (bw *BlobWriter) Digest() *digest.Digest {
	if !bw.closed {
		return nil
	}
	d := bw.digester.Digest()
	return &d
}

func DeleteBlob(img string, d digest.Digest) error {
	return os.Remove(blobPath(img, d))
}

func ReadImageLayout(img string) (*spec.ImageLayout, error) {
	b, err := ioutil.ReadFile(filepath.Join(img, spec.ImageLayoutFile))
	if err != nil {
		return nil, err
	}
	var layout spec.ImageLayout
	if err := json.Unmarshal(b, &layout); err != nil {
		return nil, err
	}
	return &layout, nil
}

func WriteImageLayout(img string, layout *spec.ImageLayout) error {
	b, err := json.Marshal(layout)
	if err != nil {
		return err
	}
	return ioutil.WriteFile(filepath.Join(img, spec.ImageLayoutFile), b, 0644)
}

func ReadIndex(img string) (*spec.Index, error) {
	b, err := ioutil.ReadFile(indexPath(img))
	if err != nil {
		return nil, err
	}
	var idx spec.Index
	if err := json.Unmarshal(b, &idx); err != nil {
		return nil, err
	}
	return &idx, nil
}

func WriteIndex(img string, idx *spec.Index) error {
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
	dst := *src
	dst.Manifests = nil
	for _, m := range src.Manifests {
		mRefName, ok := m.Annotations[RefNameAnnotation]
		if ok && mRefName == refName {
			continue
		}
		dst.Manifests = append(dst.Manifests, m)
	}
	return WriteIndex(img, &dst)
}

// PutManifestDescriptorToIndex puts a manifest descriptor to the index.
// If ref name is set and conflicts with the existing descriptors, the old ones are removed.
func PutManifestDescriptorToIndex(img string, desc *spec.Descriptor) error {
	refName, ok := desc.Annotations[RefNameAnnotation]
	if ok && refName != "" {
		if err := RemoveManifestDescriptorFromIndex(img, refName); err != nil {
			return err
		}
	}
	idx, err := ReadIndex(img)
	if err != nil {
		return err
	}
	idx.Manifests = append(idx.Manifests, *desc)
	return WriteIndex(img, idx)
}
