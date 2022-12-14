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

import (
	"context"
	"fmt"
	"strings"

	"github.com/containerd/containerd/gc"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/metadata"
	"github.com/containerd/containerd/namespaces"
	bolt "go.etcd.io/bbolt"
)

const (
	GCRefPrefix string = "containerd.io/gc.ref.network"
)

type resourceCollector struct {
	api API
	db  DB
}

type gcContext struct {
	api     API
	tx      *bolt.Tx
	removed []gc.Node
}

func NewResourceCollector(api API, db DB) metadata.Collector {
	return &resourceCollector{
		api: api,
		db:  db,
	}
}

func (rc *resourceCollector) StartCollection(ctx context.Context) (metadata.CollectionContext, error) {
	log.G(ctx).Tracef("[GC] start network collection")
	tx, err := rc.db.Begin(true)
	if err != nil {
		return nil, err
	}
	return &gcContext{
		api: rc.api,
		tx:  tx,
	}, nil
}

func (rc *resourceCollector) ReferenceLabel() string {
	return "network"
}

func (c *gcContext) All(fn func(gc.Node)) {
	// db version
	vbkt := c.tx.Bucket(bucketKeyVersion)
	if vbkt == nil {
		return
	}

	vbkt.ForEach(func(nKey, _ []byte) error {
		// namespace
		nbkt := vbkt.Bucket(nKey)
		if nbkt == nil {
			return nil
		}

		nbkt.ForEach(func(mKey, _ []byte) error {
			// manager
			mbkt := nbkt.Bucket(mKey)
			if mbkt == nil {
				return nil
			}

			// attachments
			abkt := mbkt.Bucket(bucketKeyAttachments)
			if abkt == nil {
				return nil
			}

			abkt.ForEach(func(aKey, _ []byte) error {
				// attachment
				fn(gcnode(metadata.ResourceNetwork, string(nKey), createKey(string(mKey), string(aKey))))
				return nil
			})
			return nil
		})
		return nil
	})
}

func (c *gcContext) Active(string, func(gc.Node)) {
	// noop, use the default gc that scans labels of existing containers
}

func (c *gcContext) Leased(namespace, lease string, fn func(gc.Node)) {
	// namespace
	nbkt := getBucket(c.tx, []byte(namespace))
	if nbkt == nil {
		return
	}

	nbkt.ForEach(func(mKey, _ []byte) error {
		// manager
		mbkt := nbkt.Bucket(mKey)
		if mbkt == nil {
			return nil
		}

		// leases
		lbkt := mbkt.Bucket(bucketKeyLeases)
		if lbkt == nil {
			return nil
		}

		// target lease
		bkt := lbkt.Bucket([]byte(lease))
		if bkt == nil {
			return nil
		}

		bkt.ForEach(func(aKey, _ []byte) error {
			// attachment
			fn(gcnode(metadata.ResourceNetwork, namespace, createKey(string(mKey), string(aKey))))
			return nil
		})

		return nil
	})
}

func (c *gcContext) Remove(n gc.Node) {
	if n.Type == metadata.ResourceNetwork {
		log.L.Debugf("[GC] remove %v", n)
		c.removed = append(c.removed, n)
	}
}

func (c *gcContext) Cancel() error {
	log.L.Tracef("[GC] cancel network collection")
	c.tx.Rollback()
	c.removed = nil
	return nil
}

func (c *gcContext) Finish() error {
	log.L.Tracef("[GC] finish network collection")

	for _, node := range c.removed {
		ctx := withTransactionContext(namespaces.WithNamespace(context.Background(), node.Namespace), c.tx)
		mgr, attachID := decomposeKey(node.Key)

		m := c.api.Manager(mgr)
		if m == nil {
			log.L.Errorf("manager %q not found", mgr)
			continue
		}

		att, err := m.Attachment(ctx, attachID)
		if err != nil {
			log.L.Warnf("attachment %q not found", attachID)
		}

		if err := att.Remove(ctx); err != nil {
			log.G(ctx).WithError(err).Errorf("failed to detach %q", attachID)
		}
	}

	// make sure empty namespaces are deleted in db
	cleanupNamespaces(c.tx)

	c.tx.Commit()
	c.removed = nil
	return nil
}

func cleanupNamespaces(tx *bolt.Tx) error {
	log.L.Debugf("checking and cleaning up namespaces")
	// version
	vbkt := tx.Bucket(bucketKeyVersion)
	if vbkt == nil {
		log.L.Errorf("version bucket not found")
		return nil
	}

	var nsl [][]byte
	// iterate through namespace buckets
	nc := vbkt.Cursor()
	for nk, _ := nc.First(); nk != nil; nk, _ = nc.Next() {
		empty := true

		// namespace
		nbkt := vbkt.Bucket(nk)
		if nbkt == nil {
			continue
		}

		// iterate through manager buckets
		c := nbkt.Cursor()
		for mk, _ := c.First(); mk != nil; mk, _ = c.Next() {
			// manager
			mbkt := nbkt.Bucket(mk)
			// networks
			bkt := mbkt.Bucket(bucketKeyNetworks)
			if !bucketEmpty(bkt) {
				empty = false
				break
			}
			// attachments
			abkt := mbkt.Bucket(bucketKeyAttachments)
			if !bucketEmpty(abkt) {
				empty = false
				break
			}
		}
		if empty {
			nsl = append(nsl, nk)
		}
	}

	for _, ns := range nsl {
		if err := vbkt.DeleteBucket(ns); err != nil {
			log.L.WithError(err).Debugf("failed to delete namespace bucket %s", string(ns))
		}
	}

	return nil
}

func gcnode(t gc.ResourceType, ns, key string) gc.Node {
	return gc.Node{
		Type:      t,
		Namespace: ns,
		Key:       key,
	}
}

func createKey(manager, attachID string) string {
	return fmt.Sprintf("%s/%s", manager, attachID)
}

func decomposeKey(key string) (string, string) {
	idx := strings.Index(key, "/")
	return key[:idx], key[idx+1:]
}
