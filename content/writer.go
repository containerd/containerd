package content

import (
	"log"
	"os"
	"path/filepath"

	"github.com/nightlyone/lockfile"
	"github.com/opencontainers/go-digest"
	"github.com/pkg/errors"
)

// Writer represents a write transaction against the blob store.
type Writer struct {
	cs       *Store
	fp       *os.File // opened data file
	lock     lockfile.Lockfile
	path     string // path to writer dir
	ref      string // ref key
	offset   int64
	digester digest.Digester
}

func (cw *Writer) Ref() string {
	return cw.ref
}

// Size returns the current size written.
//
// Cannot be called concurrently with `Write`. If you need need concurrent
// status, query it with `Store.Stat`.
func (cw *Writer) Size() int64 {
	return cw.offset
}

// Digest returns the current digest of the content, up to the current write.
//
// Cannot be called concurrently with `Write`.
func (cw *Writer) Digest() digest.Digest {
	return cw.digester.Digest()
}

// Write p to the transaction.
//
// Note that writes are unbuffered to the backing file. When writing, it is
// recommended to wrap in a bufio.Writer or, preferably, use io.CopyBuffer.
func (cw *Writer) Write(p []byte) (n int, err error) {
	n, err = cw.fp.Write(p)
	cw.digester.Hash().Write(p[:n])
	cw.offset += int64(len(p))
	return n, err
}

func (cw *Writer) Commit(size int64, expected digest.Digest) error {
	if err := cw.fp.Sync(); err != nil {
		return errors.Wrap(err, "sync failed")
	}

	fi, err := cw.fp.Stat()
	if err != nil {
		return errors.Wrap(err, "stat on ingest file failed")
	}

	// change to readonly, more important for read, but provides _some_
	// protection from this point on. We use the existing perms with a mask
	// only allowing reads honoring the umask on creation.
	//
	// This removes write and exec, only allowing read per the creation umask.
	if err := cw.fp.Chmod((fi.Mode() & os.ModePerm) &^ 0333); err != nil {
		return errors.Wrap(err, "failed to change ingest file permissions")
	}

	if size > 0 && size != fi.Size() {
		return errors.Errorf("failed size validation: %v != %v", fi.Size(), size)
	}

	if err := cw.fp.Close(); err != nil {
		return errors.Wrap(err, "failed closing ingest")
	}

	dgst := cw.digester.Digest()
	if expected != "" && expected != dgst {
		return errors.Errorf("unexpected digest: %v != %v", dgst, expected)
	}

	var (
		ingest = filepath.Join(cw.path, "data")
		target = cw.cs.blobPath(dgst)
	)

	// make sure parent directories of blob exist
	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		return err
	}

	// clean up!!
	defer os.RemoveAll(cw.path)

	if err := os.Rename(ingest, target); err != nil {
		if os.IsExist(err) {
			// collision with the target file!
			return nil
		}
		return err
	}

	unlock(cw.lock)
	cw.fp = nil
	return nil
}

// Close the writer, flushing any unwritten data and leaving the progress in
// tact.
//
// If one needs to resume the transaction, a new writer can be obtained from
// `ContentStore.Resume` using the same key. The write can then be continued
// from it was left off.
//
// To abandon a transaction completely, first call close then `Store.Remove` to
// clean up the associated resources.
func (cw *Writer) Close() (err error) {
	if err := unlock(cw.lock); err != nil {
		log.Printf("unlock failed: %v", err)
	}

	if cw.fp != nil {
		cw.fp.Sync()
		return cw.fp.Close()
	}

	return nil
}
