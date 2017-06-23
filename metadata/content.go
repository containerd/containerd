package metadata

import (
	"context"
	"encoding/binary"
	"io"

	"github.com/boltdb/bolt"
	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/namespaces"
	digest "github.com/opencontainers/go-digest"
)

type contentStore struct {
	content.Store
	db *bolt.DB
}

// NewContentStore returns a namespaced content store using an existing
// content store interface.
func NewContentStore(db *bolt.DB, cs content.Store) content.Store {
	return &contentStore{
		Store: cs,
		db:    db,
	}
}

func (cs *contentStore) Info(ctx context.Context, dgst digest.Digest) (content.Info, error) {
	ns, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return content.Info{}, err
	}

	var info content.Info
	if err := view(ctx, cs.db, func(tx *bolt.Tx) error {
		bkt := getContentBucket(tx, ns, dgst)
		if bkt == nil {
			return content.ErrNotFound("")
		}

		info.Digest = dgst
		return readInfo(&info, bkt)
	}); err != nil {
		return content.Info{}, err
	}

	return info, nil
}

func (cs *contentStore) Walk(ctx context.Context, fn content.WalkFunc) error {
	ns, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return err
	}

	// TODO: Batch results to keep from reading all info into memory
	var infos []content.Info
	if err := view(ctx, cs.db, func(tx *bolt.Tx) error {
		bkt := getAllContentBucket(tx, ns)
		if bkt == nil {
			return nil
		}

		return bkt.ForEach(func(k, v []byte) error {
			dgst, err := digest.Parse(string(k))
			if err != nil {
				return nil
			}
			info := content.Info{
				Digest: dgst,
			}
			if err := readInfo(&info, bkt.Bucket(k)); err != nil {
				return err
			}
			infos = append(infos, info)
			return nil
		})
	}); err != nil {
		return err
	}

	for _, info := range infos {
		if err := fn(info); err != nil {
			return err
		}
	}

	return nil
}

func (cs *contentStore) Delete(ctx context.Context, dgst digest.Digest) error {
	ns, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return err
	}

	return update(ctx, cs.db, func(tx *bolt.Tx) error {
		bkt := getContentBucket(tx, ns, dgst)
		if bkt == nil {
			return content.ErrNotFound("")
		}

		// Just remove local reference, garbage collector is responsible for
		// cleaning up on disk content
		return getAllContentBucket(tx, ns).Delete([]byte(dgst.String()))
	})
}

func (cs *contentStore) Status(ctx context.Context, re string) ([]content.Status, error) {
	_, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return nil, err
	}

	// TODO: Read status keys and match

	return cs.Store.Status(ctx, re)
}

func (cs *contentStore) Abort(ctx context.Context, ref string) error {
	_, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return err
	}

	// TODO: Read status key and delete

	return cs.Store.Abort(ctx, ref)
}

func (cs *contentStore) Writer(ctx context.Context, ref string, size int64, expected digest.Digest) (content.Writer, error) {
	ns, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return nil, err
	}

	// TODO: Create ref key

	if expected != "" {
		if err := view(ctx, cs.db, func(tx *bolt.Tx) error {
			bkt := getContentBucket(tx, ns, expected)
			if bkt != nil {
				return content.ErrExists("")
			}
			return nil
		}); err != nil {
			return nil, err
		}
	}

	// Do not use the passed in expected value here since it was
	// already checked against the user metadata. If the content
	// store has the content, it must still be written before
	// linked into the given namespace. It is possible in the future
	// to allow content which exists in content store but not
	// namespace to be linked here and returned an exist error, but
	// this would require more configuration to make secure.
	w, err := cs.Store.Writer(ctx, ref, size, "")
	if err != nil {
		return nil, err
	}

	// TODO: keep the expected in the writer to use on commit
	// when no expected is provided there.
	return &namespacedWriter{
		Writer:    w,
		namespace: ns,
		db:        cs.db,
	}, nil
}

type namespacedWriter struct {
	content.Writer
	namespace string
	db        *bolt.DB
}

func (nw *namespacedWriter) Commit(size int64, expected digest.Digest) error {
	tx, err := nw.db.Begin(true)
	if err != nil {
		return err
	}

	if err := nw.commit(tx, size, expected); err != nil {
		tx.Rollback()
		return err
	}

	return tx.Commit()
}

func (nw *namespacedWriter) commit(tx *bolt.Tx, size int64, expected digest.Digest) error {
	status, err := nw.Writer.Status()
	if err != nil {
		return err
	}
	actual := nw.Writer.Digest()

	// TODO: Handle already exists
	if err := nw.Writer.Commit(size, expected); err != nil {
		if !content.IsExists(err) {
			return err
		}
		if getContentBucket(tx, nw.namespace, actual) != nil {
			return content.ErrExists("")
		}
		// Link into this namespace
	}

	size = status.Total

	bkt, err := createContentBucket(tx, nw.namespace, actual)
	if err != nil {
		return err
	}

	sizeEncoded, err := encodeSize(size)
	if err != nil {
		return err
	}

	timeEncoded, err := status.UpdatedAt.MarshalBinary()
	if err != nil {
		return err
	}

	for _, v := range [][2][]byte{
		{bucketKeyCreatedAt, timeEncoded},
		{bucketKeySize, sizeEncoded},
	} {
		if err := bkt.Put(v[0], v[1]); err != nil {
			return err
		}
	}

	return nil
}

func (cs *contentStore) Reader(ctx context.Context, dgst digest.Digest) (io.ReadCloser, error) {
	if err := cs.checkAccess(ctx, dgst); err != nil {
		return nil, err
	}
	return cs.Store.Reader(ctx, dgst)
}

func (cs *contentStore) ReaderAt(ctx context.Context, dgst digest.Digest) (io.ReaderAt, error) {
	if err := cs.checkAccess(ctx, dgst); err != nil {
		return nil, err
	}
	return cs.Store.ReaderAt(ctx, dgst)
}

func (cs *contentStore) checkAccess(ctx context.Context, dgst digest.Digest) error {
	ns, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return err
	}

	return view(ctx, cs.db, func(tx *bolt.Tx) error {
		bkt := getContentBucket(tx, ns, dgst)
		if bkt == nil {
			return content.ErrNotFound("")
		}
		return nil
	})
}

func readInfo(info *content.Info, bkt *bolt.Bucket) error {
	return bkt.ForEach(func(k, v []byte) error {
		switch string(k) {
		case string(bucketKeyCreatedAt):
			if err := info.CommittedAt.UnmarshalBinary(v); err != nil {
				return err
			}
		case string(bucketKeySize):
			info.Size, _ = binary.Varint(v)
		}
		// TODO: Read labels
		return nil
	})
}
