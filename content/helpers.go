package content

import (
	"io"
	"os"

	"github.com/opencontainers/go-digest"
	"github.com/pkg/errors"
)

// Provider gives access to blob content by paths.
//
// Typically, this is implemented by `*Store`.
type Provider interface {
	GetPath(dgst digest.Digest) (string, error)
}

// OpenBlob opens the blob for reading identified by dgst.
//
// The opened blob may also implement seek. Callers can detect with io.Seeker.
func OpenBlob(provider Provider, dgst digest.Digest) (io.ReadCloser, error) {
	path, err := provider.GetPath(dgst)
	if err != nil {
		return nil, err
	}

	fp, err := os.Open(path)
	return fp, err
}

type Ingester interface {
	Begin(key string) (*Writer, error)
}

// WriteBlob writes data with the expected digest into the content store. If
// expected already exists, the method returns immediately and the reader will
// not be consumed.
//
// This is useful when the digest and size are known beforehand.
//
// Copy is buffered, so no need to wrap reader in buffered io.
func WriteBlob(cs Ingester, r io.Reader, size int64, expected digest.Digest) error {
	cw, err := cs.Begin(expected.Hex())
	if err != nil {
		return err
	}
	buf := bufPool.Get().([]byte)
	defer bufPool.Put(buf)

	nn, err := io.CopyBuffer(cw, r, buf)
	if err != nil {
		return err
	}

	if nn != size {
		return errors.Errorf("failed size verification: %v != %v", nn, size)
	}

	if err := cw.Commit(size, expected); err != nil {
		return err
	}

	return nil
}
