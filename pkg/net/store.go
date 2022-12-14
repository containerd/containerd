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

package net

/* Schema
v1
|--- <namespace>
     |--- <network-manager>
          |--- networks
               |--- <id>
                    |--- conflist: <bytes>
                    |--- labels
                         |--- <key>: <string>
                    |--- createdat: <datetime>
                    |--- updatedat: <datetime>
          |--- attachments
               |--- <netid>/<ctrid>/<ifid>
                    |--- args: <bytes>
										|--- conflist: <bytes>
                    |--- labels
                         |--- lease: <string>
                    |--- result: <bytes>
                    |--- createdat: <datetime>
                    |--- updatedat: <datetime>
          |--- leases
               |--- <lease-id>
                    |--- <attachment-id>
*/

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/metadata/boltutil"
	"github.com/containerd/containerd/namespaces"
	"github.com/containernetworking/cni/libcni"
	types100 "github.com/containernetworking/cni/pkg/types/100"
	"github.com/sirupsen/logrus"
	bolt "go.etcd.io/bbolt"
)

const (
	schemaVersion = "v1"
)

var (
	bucketKeyVersion     = []byte(schemaVersion)
	bucketKeyNetworks    = []byte("networks")
	bucketKeyAttachments = []byte("attachments")
	bucketKeyLeases      = []byte("leases")
	bucketKeyLabels      = []byte("labels")
	bucketKeyArgs        = []byte("args")
	bucketKeyConflist    = []byte("conflist")
	bucketKeyResult      = []byte("result")
)

type transactionKey struct{}

type DB interface {
	View(func(tx *bolt.Tx) error) error
	Update(func(tx *bolt.Tx) error) error
	Begin(writable bool) (*bolt.Tx, error)
}

type Store interface {
	CreateNetwork(ctx context.Context, r *networkRecord) error
	UpdateNetwork(ctx context.Context, r *networkRecord) error
	GetNetwork(ctx context.Context, manager, id string) (*networkRecord, error)
	DeleteNetwork(ctx context.Context, manager, id string) error
	WalkNetworks(ctx context.Context, manager string, fn func(*networkRecord) error) error
	CreateAttachment(ctx context.Context, r *attachmentRecord, creator Creator, deleter Deleter) error
	UpdateAttachment(ctx context.Context, r *attachmentRecord, updater Updater) error
	GetAttachment(ctx context.Context, manager, id string) (*attachmentRecord, error)
	DeleteAttachment(ctx context.Context, manager, id string, deleter Deleter) error
	WalkAttachments(ctx context.Context, manager, network string, fn func(*attachmentRecord) error) error
}

type networkRecord struct {
	manager   string
	config    *libcni.NetworkConfigList
	labels    map[string]string
	createdAt time.Time
	updatedAt time.Time
}

type attachmentRecord struct {
	id        string
	manager   string
	network   *libcni.NetworkConfigList
	result    *types100.Result
	labels    map[string]string
	args      AttachmentArgs
	createdAt time.Time
	updatedAt time.Time
}

// resource mutation callbacks
type Creator func(context.Context) (*types100.Result, error)
type Updater func(context.Context) error
type Deleter func(context.Context) error

func NewStore(db DB) (Store, error) {
	s := &store{
		db: db,
	}
	if err := s.Init(); err != nil {
		return nil, err
	}
	return s, nil
}

type store struct {
	db DB
}

var _ Store = (*store)(nil)

func (s *store) Init() error {
	return s.db.Update(func(tx *bolt.Tx) error {
		if _, err := tx.CreateBucketIfNotExists(bucketKeyVersion); err != nil {
			return err
		}
		return nil
	})
}

func (s *store) CreateNetwork(ctx context.Context, r *networkRecord) error {
	namespace, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return err
	}

	return update(ctx, s.db, func(tx *bolt.Tx) error {
		bkt, err := createNetworksBucket(tx, namespace, r.manager)
		if err != nil {
			return err
		}

		nbkt, err := bkt.CreateBucket([]byte(r.config.Name))
		if err != nil {
			if err == bolt.ErrBucketExists {
				err = errdefs.ErrAlreadyExists
			}
			return err
		}

		r.createdAt = time.Now().UTC()
		r.updatedAt = r.createdAt

		if err := writeNetwork(nbkt, r); err != nil {
			log.G(ctx).WithFields(logrus.Fields{
				"namespace": namespace,
				"manager":   r.manager,
				"network":   r.config.Name,
			}).WithError(err).Debugf("network creation failed")

			return err
		}

		return nil
	})
}

func (s *store) UpdateNetwork(ctx context.Context, r *networkRecord) error {
	namespace, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return err
	}

	var updated networkRecord

	return update(ctx, s.db, func(tx *bolt.Tx) error {
		bkt := getNetworksBucket(tx, namespace, r.manager)
		if bkt == nil {
			return errdefs.ErrNotFound
		}

		nbkt := bkt.Bucket([]byte(r.config.Name))
		if nbkt == nil {
			return errdefs.ErrNotFound
		}

		if err := readNetwork(&updated, nbkt); err != nil {
			return err
		}

		updated.updatedAt = time.Now().UTC()
		updated.config = r.config

		return writeNetwork(nbkt, &updated)
	})
}

func (s *store) GetNetwork(ctx context.Context, manager, id string) (*networkRecord, error) {
	namespace, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return nil, err
	}

	r := createNetworkRecord(manager, nil)

	if err := view(ctx, s.db, func(tx *bolt.Tx) error {
		bkt := getNetworksBucket(tx, namespace, manager)
		if bkt == nil {
			return errdefs.ErrNotFound
		}

		nbkt := bkt.Bucket([]byte(id))
		if nbkt == nil {
			return errdefs.ErrNotFound
		}

		return readNetwork(r, nbkt)

	}); err != nil {
		return nil, err
	}

	return r, nil
}

func (s *store) DeleteNetwork(ctx context.Context, manager, id string) error {
	namespace, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return err
	}

	return update(ctx, s.db, func(tx *bolt.Tx) error {
		bkt := getNetworksBucket(tx, namespace, manager)
		if bkt == nil {
			return errdefs.ErrNotFound
		}

		if err := bkt.DeleteBucket([]byte(id)); err != nil {
			if err == bolt.ErrBucketNotFound {
				err = errdefs.ErrNotFound
			}
			return err
		}

		return nil
	})
}

func (s *store) WalkNetworks(ctx context.Context, manager string, fn func(*networkRecord) error) error {
	namespace, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return err
	}

	return view(ctx, s.db, func(tx *bolt.Tx) error {
		bkt := getNetworksBucket(tx, namespace, manager)

		if bkt == nil {
			log.G(ctx).WithFields(logrus.Fields{
				"namespace": namespace,
				"manager":   manager,
			}).Debugf("manager bucket not found")
			return nil
		}

		return bkt.ForEach(func(k, v []byte) error {
			nbkt := bkt.Bucket(k)
			if nbkt == nil {
				return nil
			}

			r := createNetworkRecord(manager, nil)

			if err := readNetwork(r, nbkt); err != nil {
				return err
			}

			fn(r)

			return nil
		})
	})
}

func (s *store) CreateAttachment(ctx context.Context, r *attachmentRecord, creator Creator, deleter Deleter) error {
	namespace, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return err
	}

	return update(ctx, s.db, func(tx *bolt.Tx) error {
		var (
			err             error
			bkt, abkt, lbkt *bolt.Bucket
			res             *types100.Result
		)
		ctx := withTransactionContext(ctx, tx)

		bkt, err = createAttachmentsBucket(tx, namespace, r.manager)
		if err != nil {
			return err
		}

		abkt, err = bkt.CreateBucket([]byte(r.id))
		if err != nil {
			if err == bolt.ErrBucketExists {
				err = errdefs.ErrAlreadyExists
			}
			return err
		}

		res, err = creator(ctx)
		if err != nil {
			return err
		}
		defer func() {
			if err != nil {
				if derr := deleter(ctx); derr != nil {
					log.G(ctx).WithError(derr).Errorf("failed to clean up attachment %q", r.id)
				}
			}
		}()

		r.createdAt = time.Now().UTC()
		r.updatedAt = r.createdAt
		r.result = res

		if err = writeAttachment(abkt, r); err != nil {
			return err
		}

		if leaseID, ok := r.labels["lease"]; ok {
			lbkt, err = createLeasesBucket(tx, namespace, r.manager)
			if err != nil {
				return err
			}

			if err = writeLease(lbkt, leaseID, r.id); err != nil {
				return err
			}
		}

		return nil
	})
}

func (s *store) UpdateAttachment(ctx context.Context, r *attachmentRecord, updater Updater) error {
	namespace, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return err
	}

	updated := createAttachmentRecord(r.id, r.manager, r.network)

	return update(ctx, s.db, func(tx *bolt.Tx) error {
		ctx := withTransactionContext(ctx, tx)

		bkt := getAttachmentsBucket(tx, namespace, r.manager)
		if bkt == nil {
			return errdefs.ErrNotFound
		}

		abkt := bkt.Bucket([]byte(r.id))
		if abkt == nil {
			return errdefs.ErrNotFound
		}

		if err := readAttachment(updated, abkt); err != nil {
			return err
		}

		if err := validateAttachmentUpdate(updated, r); err != nil {
			return err
		}

		updated.result = r.result
		updated.updatedAt = time.Now().UTC()

		if err := writeAttachment(abkt, updated); err != nil {
			return err
		}

		return updater(ctx)
	})
}

func (s *store) GetAttachment(ctx context.Context, manager, id string) (*attachmentRecord, error) {
	namespace, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return nil, err
	}

	r := createAttachmentRecord(id, manager, nil)

	if err := view(ctx, s.db, func(tx *bolt.Tx) error {
		bkt := getAttachmentsBucket(tx, namespace, manager)

		if bkt == nil {
			return errdefs.ErrNotFound
		}

		abkt := bkt.Bucket([]byte(id))
		if abkt == nil {
			return errdefs.ErrNotFound
		}

		return readAttachment(r, abkt)

	}); err != nil {
		return nil, err
	}

	return r, nil
}

func (s *store) DeleteAttachment(ctx context.Context, manager, id string, deleter Deleter) error {
	namespace, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return err
	}
	return update(ctx, s.db, func(tx *bolt.Tx) error {
		ctx := withTransactionContext(ctx, tx)

		bkt := getAttachmentsBucket(tx, namespace, manager)
		if bkt == nil {
			return errdefs.ErrNotFound
		}

		if leaseID := queryLease(bkt, id); len(leaseID) > 0 {
			if lbkt := getLeasesBucket(tx, namespace, manager); lbkt != nil {
				removeLease(lbkt, leaseID, id)
			}
		}

		if err := bkt.DeleteBucket([]byte(id)); err != nil {
			if err == bolt.ErrBucketNotFound {
				err = errdefs.ErrNotFound
			}
			return err
		}

		return deleter(ctx)
	})
}

func (s *store) WalkAttachments(ctx context.Context, manager, network string, fn func(*attachmentRecord) error) error {
	namespace, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return err
	}

	return view(ctx, s.db, func(tx *bolt.Tx) error {
		bkt := getAttachmentsBucket(tx, namespace, manager)
		if bkt == nil {
			return nil
		}

		prefix := []byte(network + "/")
		c := bkt.Cursor()

		for k, _ := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, _ = c.Next() {
			abkt := bkt.Bucket(k)
			if abkt == nil {
				continue
			}

			r := createAttachmentRecord(string(k), manager, nil)

			if err := readAttachment(r, abkt); err != nil {
				return err
			}

			fn(r)
		}

		return nil
	})
}

func writeNetwork(bkt *bolt.Bucket, r *networkRecord) error {
	if err := boltutil.WriteTimestamps(bkt, r.createdAt, r.updatedAt); err != nil {
		return err
	}

	if err := boltutil.WriteLabels(bkt, r.labels); err != nil {
		return err
	}

	if r.config == nil {
		return fmt.Errorf("conflist is nil")
	}
	if err := bkt.Put(bucketKeyConflist, r.config.Bytes); err != nil {
		return err
	}

	return nil
}

func readNetwork(r *networkRecord, bkt *bolt.Bucket) error {
	if err := boltutil.ReadTimestamps(bkt, &r.createdAt, &r.updatedAt); err != nil {
		return err
	}

	labels, err := boltutil.ReadLabels(bkt)
	if err != nil {
		return err
	}
	r.labels = labels

	buf := bkt.Get(bucketKeyConflist)
	if buf == nil {
		return fmt.Errorf("conflist not found")
	}

	cbuf := make([]byte, len(buf))
	copy(cbuf, buf)
	conflist, err := libcni.ConfListFromBytes(cbuf)
	if err != nil {
		return err
	}
	r.config = conflist

	return err
}

func readAttachment(r *attachmentRecord, bkt *bolt.Bucket) error {
	// attachment args
	if abuf := bkt.Get(bucketKeyArgs); abuf != nil {
		if r.args.CapabilityArgs == nil {
			r.args.CapabilityArgs = make(map[string]interface{})
		}
		if r.args.PluginArgs == nil {
			r.args.PluginArgs = make(map[string]string)
		}
		if err := json.Unmarshal(abuf, &r.args); err != nil {
			return fmt.Errorf("failed to unmarshal attachment args: %w", err)
		}
	} else {
		return fmt.Errorf("attachment args key not found")
	}

	// network
	if cbuf := bkt.Get(bucketKeyConflist); cbuf != nil {
		tbuf := make([]byte, len(cbuf))
		copy(tbuf, cbuf)
		conflist, err := libcni.ConfListFromBytes(tbuf)
		if err != nil {
			return fmt.Errorf("failed to load conflist: %w", err)
		}
		r.network = conflist
	} else {
		return fmt.Errorf("network conflist key not found")
	}

	// attachment result
	if rbuf := bkt.Get(bucketKeyResult); rbuf != nil {
		result := types100.Result{}
		if err := json.Unmarshal(rbuf, &result); err != nil {
			return fmt.Errorf("failed to unmarshal attachment result: %w", err)
		}
		r.result = &result
	}

	// labels
	labels, err := boltutil.ReadLabels(bkt)
	if err != nil {
		return err
	}
	r.labels = labels

	// timestamps
	if err := boltutil.ReadTimestamps(bkt, &r.createdAt, &r.updatedAt); err != nil {
		return err
	}

	return nil
}

func writeAttachment(bkt *bolt.Bucket, r *attachmentRecord) error {
	// attachment args
	abuf, err := json.Marshal(&r.args)
	if err != nil {
		return fmt.Errorf("failed to marshal attachment args: %w", err)
	}
	if err := bkt.Put(bucketKeyArgs, abuf); err != nil {
		return fmt.Errorf("failed to write args, %w", err)
	}

	// network conflist
	if err := bkt.Put(bucketKeyConflist, r.network.Bytes); err != nil {
		return fmt.Errorf("failed to write network, %w", err)
	}

	// attachment result
	if r.result != nil {
		rbuf, err := json.Marshal(r.result)
		if err != nil {
			return fmt.Errorf("failed to marshal attachment result %w", err)
		}
		if err := bkt.Put(bucketKeyResult, rbuf); err != nil {
			return fmt.Errorf("failed to write result %w", err)
		}
	}

	// labels
	if err := boltutil.WriteLabels(bkt, r.labels); err != nil {
		return err
	}

	// timestamps
	if err := boltutil.WriteTimestamps(bkt, r.createdAt, r.updatedAt); err != nil {
		return err
	}

	return nil
}

func validateAttachmentUpdate(oldrc, newrc *attachmentRecord) error {
	if oldrc.id != newrc.id {
		return fmt.Errorf("ID changed: %w", errdefs.ErrInvalidArgument)
	}

	if oldrc.createdAt != newrc.createdAt {
		return fmt.Errorf("created changed: %w", errdefs.ErrInvalidArgument)
	}

	return nil
}

func createBucketIfNotExists(tx *bolt.Tx, keys ...[]byte) (*bolt.Bucket, error) {
	bkt, err := tx.CreateBucketIfNotExists(keys[0])
	if err != nil {
		return nil, err
	}

	for _, key := range keys[1:] {
		bkt, err = bkt.CreateBucketIfNotExists(key)
		if err != nil {
			return nil, err
		}
	}

	return bkt, nil
}

func getBucket(tx *bolt.Tx, names ...[]byte) *bolt.Bucket {
	bkt := tx.Bucket(bucketKeyVersion)
	if bkt == nil {
		return nil
	}

	for _, name := range names {
		bkt = bkt.Bucket(name)
		if bkt == nil {
			return nil
		}
	}

	return bkt
}

func bucketEmpty(bkt *bolt.Bucket) bool {
	if bkt == nil {
		return true
	}

	c := bkt.Cursor()
	for k, _ := c.First(); k != nil; k, _ = c.Next() {
		return false
	}

	return true
}

func createNetworksBucket(tx *bolt.Tx, namespace, manager string) (*bolt.Bucket, error) {
	return createBucketIfNotExists(tx, bucketKeyVersion, []byte(namespace), []byte(manager), bucketKeyNetworks)
}

func createAttachmentsBucket(tx *bolt.Tx, namespace, manager string) (*bolt.Bucket, error) {
	return createBucketIfNotExists(tx, bucketKeyVersion, []byte(namespace), []byte(manager), bucketKeyAttachments)
}

func createLeasesBucket(tx *bolt.Tx, namespace, manager string) (*bolt.Bucket, error) {
	return createBucketIfNotExists(tx, bucketKeyVersion, []byte(namespace), []byte(manager), bucketKeyLeases)
}

func getNetworksBucket(tx *bolt.Tx, namespace, manager string) *bolt.Bucket {
	return getBucket(tx, []byte(namespace), []byte(manager), bucketKeyNetworks)
}

func getAttachmentsBucket(tx *bolt.Tx, namespace, manager string) *bolt.Bucket {
	return getBucket(tx, []byte(namespace), []byte(manager), bucketKeyAttachments)
}

func getLeasesBucket(tx *bolt.Tx, namespace, manager string) *bolt.Bucket {
	return getBucket(tx, []byte(namespace), []byte(manager), bucketKeyLeases)
}

func writeLease(bkt *bolt.Bucket, leaseID string, attachID string) error {
	lbkt, err := bkt.CreateBucketIfNotExists([]byte(leaseID))
	if err != nil {
		return err
	}
	if val := lbkt.Get([]byte(attachID)); val != nil {
		return errdefs.ErrAlreadyExists
	}
	return lbkt.Put([]byte(attachID), []byte(""))
}

func removeLease(bkt *bolt.Bucket, leaseID string, attachID string) error {
	lbkt := bkt.Bucket([]byte(leaseID))
	if lbkt == nil {
		return nil
	}

	if err := lbkt.Delete([]byte(attachID)); err != nil {
		return err
	}

	if bucketEmpty(lbkt) {
		err := bkt.DeleteBucket([]byte(leaseID))
		if err != nil {
			return err
		}
	}

	return nil
}

func queryLease(bkt *bolt.Bucket, attachID string) string {
	abkt := bkt.Bucket([]byte(attachID))
	if abkt == nil {
		return ""
	}

	lbkt := abkt.Bucket(bucketKeyLabels)
	if lbkt == nil {
		return ""
	}

	v := lbkt.Get([]byte("lease"))
	if v == nil {
		return ""
	}

	return string(v)
}

func withTransactionContext(ctx context.Context, tx *bolt.Tx) context.Context {
	return context.WithValue(ctx, transactionKey{}, tx)
}

// view gets a bolt db transaction either from the context
// or starts a new one with the provided bolt database.
func view(ctx context.Context, db DB, fn func(*bolt.Tx) error) error {
	tx, ok := ctx.Value(transactionKey{}).(*bolt.Tx)
	if !ok {
		return db.View(fn)
	}
	return fn(tx)
}

// update gets a writable bolt db transaction either from the context
// or starts a new one with the provided bolt database.
func update(ctx context.Context, db DB, fn func(*bolt.Tx) error) error {
	tx, ok := ctx.Value(transactionKey{}).(*bolt.Tx)
	if !ok {
		return db.Update(fn)
	} else if !tx.Writable() {
		return fmt.Errorf("unable to use transaction from context: %w", bolt.ErrTxNotWritable)
	}
	return fn(tx)
}
