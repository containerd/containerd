package content

import (
	"context"
	"io"
	"io/ioutil"

	"github.com/opencontainers/go-digest"
	"github.com/pkg/errors"
)

// WriteBlob writes data with the expected digest into the content store. If
// expected already exists, the method returns immediately and the reader will
// not be consumed.
//
// This is useful when the digest and size are known beforehand.
//
// Copy is buffered, so no need to wrap reader in buffered io.
func WriteBlob(ctx context.Context, cs Ingester, r io.Reader, ref string, size int64, expected digest.Digest) error {
	cw, err := cs.Writer(ctx, ref)
	if err != nil {
		return err
	}

	ws, err := cw.Status()
	if err != nil {
		return err
	}

	if ws.Offset > 0 {
		// Arbitrary limitation for now. We can detect io.Seeker on r and
		// resume.
		return errors.Errorf("cannot resume already started write")
	}

	buf := BufPool.Get().([]byte)
	defer BufPool.Put(buf)

	nn, err := io.CopyBuffer(cw, r, buf)
	if err != nil {
		return err
	}

	if size > 0 && nn != size {
		return errors.Errorf("failed size verification: %v != %v", nn, size)
	}

	if err := cw.Commit(size, expected); err != nil {
		return err
	}

	return nil
}

func readFileString(path string) (string, error) {
	p, err := ioutil.ReadFile(path)
	return string(p), err
}
