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

package local

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/containerd/errdefs"
	"github.com/containerd/log"
	"github.com/opencontainers/go-digest"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/internal/fsverity"
)

// writer represents a write transaction against the blob store.
type writer struct {
	s         *store
	fp        *os.File // opened data file
	path      string   // path to writer dir
	ref       string   // ref key
	offset    int64
	total     int64
	digester  digest.Digester
	startedAt time.Time
	updatedAt time.Time

	released   bool // ref lock released
	prepared   bool
	prepFi     os.FileInfo
	prepDigest digest.Digest
}

func (w *writer) Status() (content.Status, error) {
	return content.Status{
		Ref:       w.ref,
		Offset:    w.offset,
		Total:     w.total,
		StartedAt: w.startedAt,
		UpdatedAt: w.updatedAt,
	}, nil
}

// Digest returns the current digest of the content, up to the current write.
//
// Cannot be called concurrently with `Write`.
func (w *writer) Digest() digest.Digest {
	return w.digester.Digest()
}

// Write p to the transaction.
//
// Note that writes are unbuffered to the backing file. When writing, it is
// recommended to wrap in a bufio.Writer or, preferably, use io.CopyBuffer.
func (w *writer) Write(p []byte) (n int, err error) {
	if w.fp == nil {
		return 0, fmt.Errorf("write on closed or prepared writer: %w", errdefs.ErrFailedPrecondition)
	}
	n, err = w.fp.Write(p)
	w.digester.Hash().Write(p[:n])
	w.offset += int64(len(p))
	w.updatedAt = time.Now()
	return n, err
}

// release unlocks the backend ref exactly once.
func (w *writer) release() {
	if !w.released {
		w.released = true
		w.s.unlock(w.ref)
	}
}

// prepare validates the ingest and, when supported, seals it with fsverity
// before it is published. On success the result is cached so that repeated
// calls only revalidate against the cached state. It never releases the lock.
func (w *writer) prepare(ctx context.Context, size int64, expected digest.Digest) (digest.Digest, error) {
	if w.released {
		return "", fmt.Errorf("cannot commit on closed writer: %w", errdefs.ErrFailedPrecondition)
	}
	if w.prepared {
		// The optional Precommitter contract guarantees the same arguments,
		// so reject any mismatch with the prepared state.
		if size > 0 && size != w.prepFi.Size() {
			return "", fmt.Errorf("unexpected commit size %d, expected %d: %w", w.prepFi.Size(), size, errdefs.ErrFailedPrecondition)
		}
		if expected != "" && expected != w.prepDigest {
			return "", fmt.Errorf("unexpected commit digest %s, expected %s: %w", w.prepDigest, expected, errdefs.ErrFailedPrecondition)
		}
		return w.prepDigest, nil
	}

	fp := w.fp
	if fp == nil {
		return "", fmt.Errorf("cannot commit on closed writer: %w", errdefs.ErrFailedPrecondition)
	}
	w.fp = nil

	if err := fp.Sync(); err != nil {
		fp.Close()
		return "", fmt.Errorf("sync failed: %w", err)
	}

	fi, err := fp.Stat()
	closeErr := fp.Close()
	if err != nil {
		return "", fmt.Errorf("stat on ingest file failed: %w", err)
	}
	if closeErr != nil {
		return "", fmt.Errorf("failed to close ingest file: %w", closeErr)
	}

	if size > 0 && size != fi.Size() {
		return "", fmt.Errorf("unexpected commit size %d, expected %d: %w", fi.Size(), size, errdefs.ErrFailedPrecondition)
	}

	dgst := w.digester.Digest()
	if expected != "" && expected.Algorithm() != dgst.Algorithm() && expected.Algorithm().Available() {
		// Writer was opened without a descriptor specifying the digest algorithm (but we got a non-canonical one here in commit), so we have to re-hash our now completed and closed content to compare
		var err error
		dgst, err = w.rehash(ctx, expected.Algorithm())
		if err != nil {
			return "", err
		}
	}
	if expected != "" && expected != dgst {
		return "", fmt.Errorf("unexpected commit digest %s, expected %s: %w", dgst, expected, errdefs.ErrFailedPrecondition)
	}

	// Seal the ingest before it is published (best effort)
	if w.s.integritySupported {
		ingest := filepath.Join(w.path, "data")
		if err := fsverity.Enable(ingest); err != nil {
			log.G(ctx).Warnf("failed to enable integrity for ingest %v: %s", ingest, err.Error())
		}
	}

	w.prepared = true
	w.prepFi = fi
	w.prepDigest = dgst
	return dgst, nil
}

func (w *writer) rehash(ctx context.Context, alg digest.Algorithm) (digest.Digest, error) {
	start := time.Now()
	f, err := os.Open(filepath.Join(w.path, "data"))
	if err != nil {
		return "", fmt.Errorf("failed to open ingest data for re-hashing to %s: %w", alg.String(), err)
	}
	w.digester = alg.Digester()
	_, err = io.Copy(w.digester.Hash(), f)
	f.Close()
	if err != nil {
		return "", fmt.Errorf("failed to re-hash ingest data to %s: %w", alg.String(), err)
	}
	dgst := w.digester.Digest()
	if duration := time.Since(start); duration > 250*time.Millisecond {
		log.G(ctx).WithField("digest", dgst).WithField("duration", duration).Warnf("commit for blob required expensive re-hash")
	}
	return dgst, nil
}

// Precommit validates and seals the ingest without publishing it. On
// validation failure the writer is closed and the ref unlocked, preserving
// the ingest data. On success the ref lock is kept until Commit or Close.
func (w *writer) Precommit(ctx context.Context, size int64, expected digest.Digest) error {
	if _, err := w.prepare(ctx, size, expected); err != nil {
		w.release()
		return err
	}
	return nil
}

func (w *writer) Commit(ctx context.Context, size int64, expected digest.Digest, opts ...content.Opt) error {
	// Ensure even on error the writer is fully closed and the ref unlocked
	// (only if this writer still owns it).
	defer func() {
		if w.fp != nil {
			w.fp.Close()
			w.fp = nil
		}
		w.release()
	}()

	var base content.Info
	for _, opt := range opts {
		if err := opt(&base); err != nil {
			return err
		}
	}

	dgst, err := w.prepare(ctx, size, expected)
	if err != nil {
		return err
	}
	fi := w.prepFi

	var (
		ingest    = filepath.Join(w.path, "data")
		target, _ = w.s.blobPath(dgst) // ignore error because we calculated this dgst
	)

	// make sure parent directories of blob exist
	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		return err
	}

	if _, err := os.Stat(target); err == nil {
		// collision with the target file!
		if err := os.RemoveAll(w.path); err != nil {
			log.G(ctx).WithField("ref", w.ref).WithField("path", w.path).Error("failed to remove ingest directory")
		}
		return fmt.Errorf("content %v: %w", dgst, errdefs.ErrAlreadyExists)
	}

	if err := os.Rename(ingest, target); err != nil {
		return err
	}

	if err := syncDir(filepath.Dir(target)); err != nil {
		return err
	}
	// Ingest has now been made available in the content store, attempt to complete
	// setting metadata but errors should only be logged and not returned since
	// the content store cannot be cleanly rolled back.

	commitTime := time.Now()
	if err := os.Chtimes(target, commitTime, commitTime); err != nil {
		log.G(ctx).WithField("digest", dgst).Error("failed to change file time to commit time")
	}

	// clean up!!
	if err := os.RemoveAll(w.path); err != nil {
		log.G(ctx).WithField("ref", w.ref).WithField("path", w.path).Error("failed to remove ingest directory")
	}

	if w.s.ls != nil && base.Labels != nil {
		if err := w.s.ls.Set(dgst, base.Labels); err != nil {
			log.G(ctx).WithField("digest", dgst).Error("failed to set labels")
		}
	}

	// change to readonly, more important for read, but provides _some_
	// protection from this point on. We use the existing perms with a mask
	// only allowing reads honoring the umask on creation.
	//
	// This removes write and exec, only allowing read per the creation umask.
	//
	// NOTE: Windows does not support this operation
	if runtime.GOOS != "windows" {
		if err := os.Chmod(target, (fi.Mode()&os.ModePerm)&^0333); err != nil {
			log.G(ctx).WithField("ref", w.ref).Error("failed to make readonly")
		}
	}

	return nil
}

// Close the writer, flushing any unwritten data and leaving the progress in
// tact.
//
// If one needs to resume the transaction, a new writer can be obtained from
// `Ingester.Writer` using the same key. The write can then be continued
// from it was left off.
//
// To abandon a transaction completely, first call close then `IngestManager.Abort` to
// clean up the associated resources.
func (w *writer) Close() (err error) {
	if w.released {
		return nil
	}
	if w.fp != nil {
		w.fp.Sync()
		err = w.fp.Close()
		writeTimestampFile(filepath.Join(w.path, "updatedat"), w.updatedAt)
		w.fp = nil
		w.release()
		return
	}
	if w.prepared && !w.released {
		writeTimestampFile(filepath.Join(w.path, "updatedat"), w.updatedAt)
		w.release()
	}

	return nil
}

func (w *writer) Truncate(size int64) error {
	if size != 0 {
		return errors.New("Truncate: unsupported size")
	}
	if w.fp == nil {
		return fmt.Errorf("truncate on closed or prepared writer: %w", errdefs.ErrFailedPrecondition)
	}
	w.offset = 0
	w.digester.Hash().Reset()
	if _, err := w.fp.Seek(0, io.SeekStart); err != nil {
		return err
	}
	return w.fp.Truncate(0)
}

func (w *writer) Sync() error {
	if w.fp != nil {
		return w.fp.Sync()
	}

	return nil
}
