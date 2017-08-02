package oci

import (
	"archive/tar"
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/opencontainers/go-digest"
)

// TarWriter is an interface that is implemented by archive/tar.Writer.
// (Using an interface allows hooking)
type TarWriter interface {
	io.WriteCloser
	Flush() error
	WriteHeader(hdr *tar.Header) error
}

// Tar is ImageDriver for TAR representation of an OCI image.
func Tar(w TarWriter) ImageDriver {
	return &tarDriver{
		w: w,
	}
}

type tarDriver struct {
	w TarWriter
}

func (d *tarDriver) Init() error {
	headers := []tar.Header{
		{
			Name:     "blobs/",
			Mode:     0755,
			Typeflag: tar.TypeDir,
		},
		{
			Name:     "blobs/" + string(digest.Canonical) + "/",
			Mode:     0755,
			Typeflag: tar.TypeDir,
		},
	}
	for _, h := range headers {
		if err := d.w.WriteHeader(&h); err != nil {
			return err
		}
	}
	return nil
}

func (d *tarDriver) Remove(path string) error {
	return errors.New("Tar does not support Remove")
}

func (d *tarDriver) Reader(path string) (io.ReadCloser, error) {
	// because tar does not support random access
	return nil, errors.New("Tar does not support Reader")
}

func (d *tarDriver) Writer(path string, perm os.FileMode) (io.WriteCloser, error) {
	name := filepath.ToSlash(path)
	return &tarDriverWriter{
		w:    d.w,
		name: name,
		mode: int64(perm),
	}, nil
}

// tarDriverWriter is used for writing non-blob files
// (e.g. oci-layout, index.json)
type tarDriverWriter struct {
	bytes.Buffer
	w    TarWriter
	name string
	mode int64
}

func (w *tarDriverWriter) Close() error {
	if err := w.w.WriteHeader(&tar.Header{
		Name:     w.name,
		Mode:     w.mode,
		Size:     int64(w.Len()),
		Typeflag: tar.TypeReg,
	}); err != nil {
		return err
	}
	n, err := io.Copy(w.w, w)
	if err != nil {
		return err
	}
	if n < int64(w.Len()) {
		return io.ErrShortWrite
	}
	return w.w.Flush()
}

func (d *tarDriver) BlobWriter(algo digest.Algorithm) (BlobWriter, error) {
	return &tarBlobWriter{
		w:        d.w,
		digester: algo.Digester(),
	}, nil
}

// tarBlobWriter implements BlobWriter.
type tarBlobWriter struct {
	w        TarWriter
	digester digest.Digester
	buf      bytes.Buffer // TODO: use tmp file for large buffer?
}

// Write implements io.Writer.
func (bw *tarBlobWriter) Write(b []byte) (int, error) {
	n, err := bw.buf.Write(b)
	if err != nil {
		return n, err
	}
	return bw.digester.Hash().Write(b)
}

func (bw *tarBlobWriter) Commit(size int64, expected digest.Digest) error {
	path := "blobs/" + bw.digester.Digest().Algorithm().String() + "/" + bw.digester.Digest().Hex()
	if err := bw.w.WriteHeader(&tar.Header{
		Name:     path,
		Mode:     0444,
		Size:     int64(bw.buf.Len()),
		Typeflag: tar.TypeReg,
	}); err != nil {
		return err
	}
	n, err := io.Copy(bw.w, &bw.buf)
	if err != nil {
		return err
	}
	if n < int64(bw.buf.Len()) {
		return io.ErrShortWrite
	}
	if size > 0 && size != n {
		return ErrUnexpectedSize{Expected: size, Actual: n}
	}
	if expected != "" && bw.digester.Digest() != expected {
		return ErrUnexpectedDigest{Expected: expected, Actual: bw.digester.Digest()}
	}
	return bw.w.Flush()
}

func (bw *tarBlobWriter) Close() error {
	// we don't close bw.w (reused for writing another blob)
	return bw.w.Flush()
}

func (bw *tarBlobWriter) Digest() digest.Digest {
	return bw.digester.Digest()
}
