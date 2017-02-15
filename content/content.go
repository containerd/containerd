package content

import (
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/docker/containerd/log"
	"github.com/nightlyone/lockfile"
	"github.com/opencontainers/go-digest"
	"github.com/pkg/errors"
)

var (
	ErrBlobNotFound = errors.New("blob not found")

	bufPool = sync.Pool{
		New: func() interface{} {
			return make([]byte, 32<<10)
		},
	}
)

// Store is digest-keyed store for content. All data written into the store is
// stored under a verifiable digest.
//
// Store can generally support multi-reader, single-writer ingest of data,
// including resumable ingest.
type Store struct {
	root string
}

func Open(root string) (*Store, error) {
	if err := os.MkdirAll(filepath.Join(root, "ingest"), 0777); err != nil && !os.IsExist(err) {
		return nil, err
	}

	return &Store{
		root: root,
	}, nil
}

type Status struct {
	Ref     string
	Size    int64
	ModTime time.Time
	Meta    interface{}
}

func (cs *Store) Exists(dgst digest.Digest) (bool, error) {
	if _, err := os.Stat(cs.blobPath(dgst)); err != nil {
		if !os.IsNotExist(err) {
			return false, err
		}

		return false, nil
	}

	return true, nil
}

func (cs *Store) GetPath(dgst digest.Digest) (string, error) {
	p := cs.blobPath(dgst)
	if _, err := os.Stat(p); err != nil {
		if os.IsNotExist(err) {
			return "", ErrBlobNotFound
		}

		return "", err
	}

	return p, nil
}

// Delete removes a blob by its digest.
//
// While this is safe to do concurrently, safe exist-removal logic must hold
// some global lock on the store.
func (cs *Store) Delete(dgst digest.Digest) error {
	if err := os.RemoveAll(cs.blobPath(dgst)); err != nil {
		if !os.IsNotExist(err) {
			return err
		}

		return nil
	}

	return nil
}

func (cs *Store) blobPath(dgst digest.Digest) string {
	return filepath.Join(cs.root, "blobs", dgst.Algorithm().String(), dgst.Hex())
}

// Stat returns the current status of a blob by the ingest ref.
func (cs *Store) Stat(ref string) (Status, error) {
	dp := filepath.Join(cs.ingestRoot(ref), "data")
	return cs.stat(dp)
}

// stat works like stat above except uses the path to the ingest.
func (cs *Store) stat(ingestPath string) (Status, error) {
	dp := filepath.Join(ingestPath, "data")
	dfi, err := os.Stat(dp)
	if err != nil {
		return Status{}, err
	}

	ref, err := readFileString(filepath.Join(ingestPath, "ref"))
	if err != nil {
		return Status{}, err
	}

	return Status{
		Ref:     ref,
		Size:    dfi.Size(),
		ModTime: dfi.ModTime(),
	}, nil
}

func (cs *Store) Active() ([]Status, error) {
	ip := filepath.Join(cs.root, "ingest")

	fp, err := os.Open(ip)
	if err != nil {
		return nil, err
	}

	fis, err := fp.Readdir(-1)
	if err != nil {
		return nil, err
	}

	var active []Status
	for _, fi := range fis {
		p := filepath.Join(ip, fi.Name())
		stat, err := cs.stat(p)
		if err != nil {
			if !os.IsNotExist(err) {
				return nil, err
			}

			// TODO(stevvooe): This is a common error if uploads are being
			// completed while making this listing. Need to consider taking a
			// lock on the whole store to coordinate this aspect.
			//
			// Another option is to cleanup downloads asynchronously and
			// coordinate this method with the cleanup process.
			//
			// For now, we just skip them, as they really don't exist.
			continue
		}

		active = append(active, stat)
	}

	return active, nil
}

// TODO(stevvooe): Allow querying the set of blobs in the blob store.

// WalkFunc defines the callback for a blob walk.
//
// TODO(stevvooe): Remove the file info. Just need size and modtime. Perhaps,
// not a huge deal, considering we have a path, but let's not just let this one
// go without scrutiny.
type WalkFunc func(path string, fi os.FileInfo, dgst digest.Digest) error

func (cs *Store) Walk(fn WalkFunc) error {
	root := filepath.Join(cs.root, "blobs")
	var alg digest.Algorithm
	return filepath.Walk(root, func(path string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !fi.IsDir() && !alg.Available() {
			return nil
		}

		// TODO(stevvooe): There are few more cases with subdirs that should be
		// handled in case the layout gets corrupted. This isn't strict enough
		// an may spew bad data.

		if path == root {
			return nil
		}
		if filepath.Dir(path) == root {
			alg = digest.Algorithm(filepath.Base(path))

			if !alg.Available() {
				alg = ""
				return filepath.SkipDir
			}

			// descending into a hash directory
			return nil
		}

		dgst := digest.NewDigestFromHex(alg.String(), filepath.Base(path))
		if err := dgst.Validate(); err != nil {
			// log error but don't report
			log.L.WithError(err).WithField("path", path).Error("invalid digest for blob path")
			// if we see this, it could mean some sort of corruption of the
			// store or extra paths not expected previously.
		}

		return fn(path, fi, dgst)
	})
}

// Begin starts a new write transaction against the blob store.
//
// The argument `ref` is used to identify the transaction. It must be a valid
// path component, meaning it has no `/` characters and no `:` (we'll ban
// others fs characters, as needed).
func (cs *Store) Begin(ref string) (*Writer, error) {
	path, refp, data, lock, err := cs.ingestPaths(ref)
	if err != nil {
		return nil, err
	}

	// use single path mkdir for this to ensure ref is only base path, in
	// addition to validation above.
	if err := os.Mkdir(path, 0755); err != nil {
		return nil, err
	}

	if err := tryLock(lock); err != nil {
		return nil, err
	}

	// write the ref to a file for later use
	if err := ioutil.WriteFile(refp, []byte(ref), 0666); err != nil {
		return nil, err
	}

	fp, err := os.OpenFile(data, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0666)
	if err != nil {
		return nil, errors.Wrap(err, "failed to open data file")
	}
	defer fp.Close()

	// re-open the file in append mode
	fp, err = os.OpenFile(data, os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		return nil, errors.Wrap(err, "error opening for append")
	}

	return &Writer{
		cs:       cs,
		fp:       fp,
		lock:     lock,
		path:     path,
		digester: digest.Canonical.Digester(),
	}, nil
}

func (cs *Store) Resume(ref string) (*Writer, error) {
	path, refp, data, lock, err := cs.ingestPaths(ref)
	if err != nil {
		return nil, err
	}

	if err := tryLock(lock); err != nil {
		return nil, err
	}

	refraw, err := readFileString(refp)
	if err != nil {
		return nil, errors.Wrap(err, "could not read ref")
	}

	if ref != refraw {
		// NOTE(stevvooe): This is fairly catastrophic. Either we have some
		// layout corruption or a hash collision for the ref key.
		return nil, errors.Wrapf(err, "ref key does not match: %v != %v", ref, refraw)
	}

	digester := digest.Canonical.Digester()

	// slow slow slow!!, send to goroutine or use resumable hashes
	fp, err := os.Open(data)
	if err != nil {
		return nil, err
	}
	defer fp.Close()

	p := bufPool.Get().([]byte)
	defer bufPool.Put(p)

	offset, err := io.CopyBuffer(digester.Hash(), fp, p)
	if err != nil {
		return nil, err
	}

	fp1, err := os.OpenFile(data, os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, errors.Wrap(err, "ingest does not exist")
		}

		return nil, errors.Wrap(err, "error opening for append")
	}

	return &Writer{
		cs:       cs,
		fp:       fp1,
		lock:     lock,
		ref:      ref,
		path:     path,
		offset:   offset,
		digester: digester,
	}, nil
}

// Remove an active transaction keyed by ref.
func (cs *Store) Remove(ref string) error {
	root := cs.ingestRoot(ref)
	if err := os.RemoveAll(root); err != nil {
		if os.IsNotExist(err) {
			return nil
		}

		return err
	}

	return nil
}

func (cs *Store) ingestRoot(ref string) string {
	dgst := digest.FromString(ref)
	return filepath.Join(cs.root, "ingest", dgst.Hex())
}

// ingestPaths are returned, including the lockfile. The paths are the following:
//
// - root: entire ingest directory
// - ref: name of the starting ref, must be unique
// - data: file where data is written
// - lock: lock file location
//
func (cs *Store) ingestPaths(ref string) (string, string, string, lockfile.Lockfile, error) {
	var (
		fp = cs.ingestRoot(ref)
		rp = filepath.Join(fp, "ref")
		lp = filepath.Join(fp, "lock")
		dp = filepath.Join(fp, "data")
	)

	lock, err := lockfile.New(lp)
	if err != nil {
		return "", "", "", "", errors.Wrapf(err, "error creating lockfile %v", lp)
	}

	return fp, rp, dp, lock, nil
}

func readFileString(path string) (string, error) {
	p, err := ioutil.ReadFile(path)
	return string(p), err
}
