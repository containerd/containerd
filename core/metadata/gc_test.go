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

package metadata

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"path/filepath"
	"sort"
	"testing"
	"text/tabwriter"
	"time"

	"github.com/containerd/log/logtest"
	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	bolt "go.etcd.io/bbolt"

	"github.com/containerd/containerd/v2/core/metadata/boltutil"
	"github.com/containerd/containerd/v2/pkg/gc"
)

func TestResourceMax(t *testing.T) {
	if ResourceContent != resourceContentFlat&gc.ResourceMax {
		t.Fatalf("Invalid flat content type: %d (max %d)", resourceContentFlat, gc.ResourceMax)
	}
	if ResourceSnapshot != resourceSnapshotFlat&gc.ResourceMax {
		t.Fatalf("Invalid flat snapshot type: %d (max %d)", resourceSnapshotFlat, gc.ResourceMax)
	}
}

func TestGCRoots(t *testing.T) {
	db, err := newDatabase(t)
	require.NoError(t, err)

	alters := []alterFunc{
		addImage("ns1", "image1", dgst(1), nil),
		addImage("ns1", "image2", dgst(2), labelmap(string(labelGCSnapRef)+"overlay", "sn2")),
		addImage("ns2", "image3", dgst(10), labelmap(
			string(labelGCContentRef), dgst(11).String(),
			string(labelGCImageRef), "image4",
		)),
		addImage("ns2", "image4", dgst(12), labelmap(string(labelGCExpire), time.Now().Format(time.RFC3339))),
		addImage("ns2", "image5", dgst(13), labelmap(string(labelGCExpire), time.Now().Format(time.RFC3339))),
		addContainer("ns1", "container1", "overlay", "sn4", nil),
		addContainer("ns1", "container2", "overlay", "sn5", labelmap(string(labelGCSnapRef)+"overlay", "sn6")),
		addContainer("ns1", "container3", "overlay", "sn7", labelmap(
			string(labelGCSnapRef)+"overlay/anything-1", "sn8",
			string(labelGCSnapRef)+"overlay/anything-2", "sn9",
			string(labelGCContentRef), dgst(7).String())),
		addContainer("ns1", "container4", "", "", labelmap(
			string(labelGCContentRef)+".0", dgst(8).String(),
			string(labelGCContentRef)+".1", dgst(9).String())),
		addContent("ns1", dgst(1), nil),
		addContent("ns1", dgst(2), nil),
		addContent("ns1", dgst(3), nil),
		addContent("ns2", dgst(1), nil),
		addContent("ns2", dgst(2), labelmap(string(labelGCRoot), "always")),
		addContent("ns2", dgst(8), nil),
		addContent("ns2", dgst(9), nil),
		addIngest("ns1", "ingest-1", "", nil),       // will be seen as expired
		addIngest("ns1", "ingest-2", "", timeIn(0)), // expired
		addIngest("ns1", "ingest-3", "", timeIn(time.Hour)),
		addIngest("ns2", "ingest-4", "", nil),
		addIngest("ns2", "ingest-5", dgst(8), nil),
		addIngest("ns2", "ingest-6", "", nil),      // added to expired lease
		addIngest("ns2", "ingest-7", dgst(9), nil), // added to expired lease
		addSnapshot("ns1", "overlay", "sn1", "", nil),
		addSnapshot("ns1", "overlay", "sn2", "", nil),
		addSnapshot("ns1", "overlay", "sn3", "", labelmap(string(labelGCRoot), "always")),
		addSnapshot("ns1", "overlay", "sn4", "", nil),
		addSnapshot("ns1", "overlay", "sn5", "", nil),
		addSnapshot("ns1", "overlay", "sn6", "", nil),
		addSnapshot("ns1", "overlay", "sn7", "", nil),
		addSnapshot("ns1", "overlay", "sn8", "", nil),
		addSnapshot("ns1", "overlay", "sn9", "", nil),
		addLeaseSnapshot("ns2", "l1", "overlay", "sn5"),
		addLeaseSnapshot("ns2", "l2", "overlay", "sn6"),
		addLeaseContent("ns2", "l1", dgst(4)),
		addLeaseContent("ns2", "l2", dgst(5)),
		addLease("ns2", "l3", labelmap(string(labelGCExpire), time.Now().Add(time.Hour).Format(time.RFC3339))),
		addLeaseContent("ns2", "l3", dgst(6)),
		addLeaseSnapshot("ns2", "l3", "overlay", "sn7"),
		addLeaseImage("ns2", "l3", "image5"),
		addLeaseIngest("ns2", "l3", "ingest-4"),
		addLeaseIngest("ns2", "l3", "ingest-5"),
		addLease("ns2", "l4", labelmap(string(labelGCExpire), time.Now().Format(time.RFC3339))),
		addLeaseContent("ns2", "l4", dgst(7)),
		addLeaseSnapshot("ns2", "l4", "overlay", "sn8"),
		addLeaseImage("ns2", "l4", "image4"),
		addLeaseIngest("ns2", "l4", "ingest-6"),
		addLeaseIngest("ns2", "l4", "ingest-7"),

		addLease("ns3", "l1", labelmap(string(labelGCFlat), time.Now().Add(time.Hour).Format(time.RFC3339))),
		addLeaseContent("ns3", "l1", dgst(1)),
		addLeaseSnapshot("ns3", "l1", "overlay", "sn1"),
		addLeaseImage("ns3", "l1", "image1"),
		addLeaseIngest("ns3", "l1", "ingest-1"),

		addSandbox("ns3", "sandbox1", nil),
		addSandbox("ns4", "sandbox1", labelmap(string(labelGCSnapRef)+"overlay", "sn1")),
	}

	expected := []gc.Node{
		gcnode(ResourceContent, "ns2", dgst(2).String()),
		gcnode(ResourceContent, "ns2", dgst(4).String()),
		gcnode(ResourceContent, "ns2", dgst(5).String()),
		gcnode(ResourceContent, "ns2", dgst(6).String()),
		gcnode(ResourceSnapshot, "ns1", "overlay/sn3"),
		gcnode(ResourceSnapshot, "ns2", "overlay/sn5"),
		gcnode(ResourceSnapshot, "ns2", "overlay/sn6"),
		gcnode(ResourceSnapshot, "ns2", "overlay/sn7"),
		gcnode(ResourceSnapshot, "ns4", "overlay/sn1"),
		gcnode(ResourceContainer, "ns1", "container1"),
		gcnode(ResourceContainer, "ns1", "container2"),
		gcnode(ResourceContainer, "ns1", "container3"),
		gcnode(ResourceContainer, "ns1", "container4"),
		gcnode(ResourceImage, "ns1", "image1"),
		gcnode(ResourceImage, "ns1", "image2"),
		gcnode(ResourceImage, "ns2", "image3"),
		gcnode(ResourceImage, "ns2", "image5"),
		gcnode(ResourceLease, "ns2", "l1"),
		gcnode(ResourceLease, "ns2", "l2"),
		gcnode(ResourceLease, "ns2", "l3"),
		gcnode(ResourceIngest, "ns1", "ingest-3"),
		gcnode(ResourceIngest, "ns2", "ingest-4"),
		gcnode(ResourceIngest, "ns2", "ingest-5"),
		gcnode(ResourceLease, "ns3", "l1"),
		gcnode(ResourceIngest, "ns3", "ingest-1"),
		gcnode(resourceContentFlat, "ns3", dgst(1).String()),
		gcnode(resourceSnapshotFlat, "ns3", "overlay/sn1"),
		gcnode(resourceImageFlat, "ns3", "image1"),
	}

	if err := db.Update(func(tx *bolt.Tx) error {
		v1bkt, err := tx.CreateBucketIfNotExists(bucketKeyVersion)
		if err != nil {
			return err
		}
		for _, alter := range alters {
			if err := alter(v1bkt); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("Update failed: %+v", err)
	}

	ctx := context.Background()

	checkNodeC(ctx, t, db, expected, func(ctx context.Context, tx *bolt.Tx, nc chan<- gc.Node) error {
		return startGCContext(ctx, nil).scanRoots(ctx, tx, nc)
	})
}

func TestGCRemove(t *testing.T) {
	db, err := newDatabase(t)
	require.NoError(t, err)

	alters := []alterFunc{
		addImage("ns1", "image1", dgst(1), nil),
		addImage("ns1", "image2", dgst(2), labelmap(string(labelGCSnapRef)+"overlay", "sn2")),
		addContainer("ns1", "container1", "overlay", "sn4", nil),
		addContent("ns1", dgst(1), nil),
		addContent("ns1", dgst(2), nil),
		addContent("ns1", dgst(3), nil),
		addContent("ns2", dgst(1), nil),
		addContent("ns2", dgst(2), labelmap(string(labelGCRoot), "always")),
		addIngest("ns1", "ingest-1", "", nil),
		addIngest("ns2", "ingest-2", "", timeIn(0)),
		addSnapshot("ns1", "overlay", "sn1", "", nil),
		addSnapshot("ns1", "overlay", "sn2", "", nil),
		addSnapshot("ns1", "overlay", "sn3", "", labelmap(string(labelGCRoot), "always")),
		addSnapshot("ns1", "overlay", "sn4", "", nil),
		addSnapshot("ns2", "overlay", "sn1", "", nil),
		addLease("ns1", "l1", labelmap(string(labelGCExpire), time.Now().Add(time.Hour).Format(time.RFC3339))),
		addLease("ns2", "l2", labelmap(string(labelGCExpire), time.Now().Format(time.RFC3339))),
	}

	all := []gc.Node{
		gcnode(ResourceContent, "ns1", dgst(1).String()),
		gcnode(ResourceContent, "ns1", dgst(2).String()),
		gcnode(ResourceContent, "ns1", dgst(3).String()),
		gcnode(ResourceContent, "ns2", dgst(1).String()),
		gcnode(ResourceContent, "ns2", dgst(2).String()),
		gcnode(ResourceSnapshot, "ns1", "overlay/sn1"),
		gcnode(ResourceSnapshot, "ns1", "overlay/sn2"),
		gcnode(ResourceSnapshot, "ns1", "overlay/sn3"),
		gcnode(ResourceSnapshot, "ns1", "overlay/sn4"),
		gcnode(ResourceSnapshot, "ns2", "overlay/sn1"),
		gcnode(ResourceImage, "ns1", "image1"),
		gcnode(ResourceImage, "ns1", "image2"),
		gcnode(ResourceLease, "ns1", "l1"),
		gcnode(ResourceLease, "ns2", "l2"),
		gcnode(ResourceIngest, "ns1", "ingest-1"),
		gcnode(ResourceIngest, "ns2", "ingest-2"),
	}

	var deleted, remaining []gc.Node
	for i, n := range all {
		if i%2 == 0 {
			deleted = append(deleted, n)
		} else {
			remaining = append(remaining, n)
		}
	}

	if err := db.Update(func(tx *bolt.Tx) error {
		v1bkt, err := tx.CreateBucketIfNotExists(bucketKeyVersion)
		if err != nil {
			return err
		}
		for _, alter := range alters {
			if err := alter(v1bkt); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("Update failed: %+v", err)
	}

	ctx := context.Background()
	c := startGCContext(ctx, nil)

	checkNodes(ctx, t, db, all, func(ctx context.Context, tx *bolt.Tx, fn func(context.Context, gc.Node) error) error {
		return c.scanAll(ctx, tx, fn)
	})
	if t.Failed() {
		t.Fatal("Scan all failed")
	}

	if err := db.Update(func(tx *bolt.Tx) error {
		for _, n := range deleted {
			if _, err := c.remove(ctx, tx, n); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("Update failed: %+v", err)
	}

	checkNodes(ctx, t, db, remaining, func(ctx context.Context, tx *bolt.Tx, fn func(context.Context, gc.Node) error) error {
		return c.scanAll(ctx, tx, fn)
	})
}

func TestGCRefs(t *testing.T) {
	db, err := newDatabase(t)
	require.NoError(t, err)

	alters := []alterFunc{
		addContent("ns1", dgst(1), nil),
		addContent("ns1", dgst(2), nil),
		addContent("ns1", dgst(3), nil),
		addContent("ns1", dgst(4), labelmap(string(labelGCContentRef), dgst(1).String())),
		addContent("ns1", dgst(5), labelmap(
			string(labelGCContentRef)+".anything-1", dgst(2).String(),
			string(labelGCContentRef)+".anything-2", dgst(3).String())),
		addContent("ns1", dgst(6), labelmap(string(labelGCContentRef)+"bad", dgst(1).String())),
		addContent("ns1", dgst(7), labelmap(
			string(labelGCContentRef)+"/anything-1", dgst(2).String(),
			string(labelGCContentRef)+"/anything-2", dgst(3).String(),
			string(labelGCContainerBackRef), "container3")),
		addContent("ns2", dgst(1), nil),
		addContent("ns2", dgst(2), nil),
		addContainer("ns1", "container1", "overlay", "sn4", nil),
		addContainer("ns1", "container2", "overlay", "sn5", labelmap(string(labelGCSnapRef)+"overlay", "sn6")),
		addContainer("ns1", "container3", "overlay", "sn7", labelmap(
			string(labelGCSnapRef)+"overlay/anything-1", "sn8",
			string(labelGCSnapRef)+"overlay/anything-2", "sn9",
			string(labelGCContentRef), dgst(7).String())),
		addContainer("ns1", "container4", "", "", labelmap(
			string(labelGCContentRef)+".0", dgst(8).String(),
			string(labelGCContentRef)+".1", dgst(9).String())),
		addImage("ns1", "image1", dgst(3), nil),
		addImage("ns1", "image2", dgst(4), labelmap(
			string(labelGCImageRef)+".anything", "image1",
			string(labelGCContentRef)+".anotherimage", dgst(5).String())),
		addIngest("ns1", "ingest-1", "", nil),
		addIngest("ns2", "ingest-2", dgst(8), nil),
		addSnapshot("ns1", "overlay", "sn1", "", nil),
		addSnapshot("ns1", "overlay", "sn2", "sn1", nil),
		addSnapshot("ns1", "overlay", "sn3", "sn2", nil),
		addSnapshot("ns1", "overlay", "sn4", "", labelmap(
			string(labelGCSnapRef)+"btrfs", "sn1",
			string(labelGCSnapRef)+"overlay", "sn1")),
		addSnapshot("ns1", "overlay", "sn5", "", labelmap(
			string(labelGCSnapRef)+"overlay/anything-1", "sn1",
			string(labelGCSnapRef)+"overlay/anything-2", "sn2")),
		addSnapshot("ns1", "btrfs", "sn1", "", nil),
		addSnapshot("ns2", "overlay", "sn1", "", nil),
		addSnapshot("ns2", "overlay", "sn2", "sn1", nil),
		addSnapshot("ns2", "overlay", "sn3", "", labelmap(
			string(labelGCContentRef), dgst(1).String(),
			string(labelGCContentRef)+".keep-me", dgst(6).String())),

		// Test flat references don't follow label references
		addContent("ns3", dgst(1), nil),
		addContent("ns3", dgst(2), labelmap(string(labelGCContentRef)+".0", dgst(1).String())),

		// Back references
		addContent("ns1", dgst(10), labelmap(
			string(labelGCContainerBackRef), "container1",
			string(labelGCContentBackRef), dgst(5).String(),
			string(labelGCImageBackRef), "image2",
			string(labelGCSnapBackRef)+"btrfs", "sn1")),
		addContent("ns3", dgst(10), labelmap(string(labelGCContainerBackRef), "container1")),
		addImage("ns1", "image3", dgst(10), labelmap(
			string(labelGCImageBackRef), "image2",
			string(labelGCExpire), time.Now().Add(time.Hour).Format(time.RFC3339))), // If not expired, reference is irrelevant as it is already a root
		addImage("ns1", "image4", dgst(10), labelmap(
			string(labelGCImageBackRef), "image2",
			string(labelGCExpire), time.Now().Add(-1*time.Hour).Format(time.RFC3339))),

		addSnapshot("ns3", "overlay", "sn1", "", nil),
		addSnapshot("ns3", "overlay", "sn2", "sn1", nil),
		addSnapshot("ns3", "overlay", "sn3", "", labelmap(string(labelGCSnapRef)+"btrfs", "sn1", string(labelGCSnapRef)+"overlay", "sn1")),
	}

	refs := map[gc.Node][]gc.Node{
		gcnode(ResourceContent, "ns1", dgst(1).String()): nil,
		gcnode(ResourceContent, "ns1", dgst(2).String()): nil,
		gcnode(ResourceContent, "ns1", dgst(3).String()): nil,
		gcnode(ResourceContent, "ns1", dgst(4).String()): {
			gcnode(ResourceContent, "ns1", dgst(1).String()),
		},
		gcnode(ResourceContent, "ns1", dgst(5).String()): {
			gcnode(ResourceContent, "ns1", dgst(10).String()),
			gcnode(ResourceContent, "ns1", dgst(2).String()),
			gcnode(ResourceContent, "ns1", dgst(3).String()),
		},
		gcnode(ResourceContent, "ns1", dgst(6).String()): nil,
		gcnode(ResourceContent, "ns1", dgst(7).String()): {
			gcnode(ResourceContent, "ns1", dgst(2).String()),
			gcnode(ResourceContent, "ns1", dgst(3).String()),
		},
		gcnode(ResourceContent, "ns2", dgst(1).String()): nil,
		gcnode(ResourceContent, "ns2", dgst(2).String()): nil,
		gcnode(ResourceSnapshot, "ns1", "overlay/sn1"):   nil,
		gcnode(ResourceSnapshot, "ns1", "overlay/sn2"): {
			gcnode(ResourceSnapshot, "ns1", "overlay/sn1"),
		},
		gcnode(ResourceSnapshot, "ns1", "overlay/sn3"): {
			gcnode(ResourceSnapshot, "ns1", "overlay/sn2"),
		},
		gcnode(ResourceSnapshot, "ns1", "overlay/sn4"): {
			gcnode(ResourceSnapshot, "ns1", "btrfs/sn1"),
			gcnode(ResourceSnapshot, "ns1", "overlay/sn1"),
		},
		gcnode(ResourceSnapshot, "ns1", "overlay/sn5"): {
			gcnode(ResourceSnapshot, "ns1", "overlay/sn1"),
			gcnode(ResourceSnapshot, "ns1", "overlay/sn2"),
		},
		gcnode(ResourceSnapshot, "ns1", "btrfs/sn1"): {
			gcnode(ResourceContent, "ns1", dgst(10).String()),
		},
		gcnode(ResourceSnapshot, "ns2", "overlay/sn1"): nil,
		gcnode(ResourceSnapshot, "ns2", "overlay/sn2"): {
			gcnode(ResourceSnapshot, "ns2", "overlay/sn1"),
		},
		gcnode(ResourceSnapshot, "ns2", "overlay/sn3"): {
			gcnode(ResourceContent, "ns2", dgst(1).String()),
			gcnode(ResourceContent, "ns2", dgst(6).String()),
		},
		gcnode(ResourceContainer, "ns1", "container1"): {
			gcnode(ResourceContent, "ns1", dgst(10).String()),
			gcnode(ResourceSnapshot, "ns1", "overlay/sn4"),
		},
		gcnode(ResourceContainer, "ns1", "container2"): {
			gcnode(ResourceSnapshot, "ns1", "overlay/sn5"),
			gcnode(ResourceSnapshot, "ns1", "overlay/sn6"),
		},
		gcnode(ResourceContainer, "ns1", "container3"): {
			gcnode(ResourceContent, "ns1", dgst(7).String()),
			gcnode(ResourceContent, "ns1", dgst(7).String()), // Referenced by label and backreference
			gcnode(ResourceSnapshot, "ns1", "overlay/sn7"),
			gcnode(ResourceSnapshot, "ns1", "overlay/sn8"),
			gcnode(ResourceSnapshot, "ns1", "overlay/sn9"),
		},
		gcnode(ResourceContainer, "ns1", "container4"): {
			gcnode(ResourceContent, "ns1", dgst(8).String()),
			gcnode(ResourceContent, "ns1", dgst(9).String()),
		},
		gcnode(ResourceImage, "ns1", "image1"): {
			gcnode(ResourceContent, "ns1", dgst(3).String()),
		},
		gcnode(ResourceImage, "ns1", "image2"): {
			gcnode(ResourceContent, "ns1", dgst(10).String()),
			gcnode(ResourceContent, "ns1", dgst(4).String()),
			gcnode(ResourceContent, "ns1", dgst(5).String()),
			gcnode(ResourceImage, "ns1", "image1"),
			gcnode(ResourceImage, "ns1", "image4"),
		},
		gcnode(ResourceIngest, "ns1", "ingest-1"): nil,
		gcnode(ResourceIngest, "ns2", "ingest-2"): {
			gcnode(ResourceContent, "ns2", dgst(8).String()),
		},
		gcnode(resourceSnapshotFlat, "ns3", "overlay/sn2"): {
			gcnode(resourceSnapshotFlat, "ns3", "overlay/sn1"),
		},
		gcnode(ResourceSnapshot, "ns3", "overlay/sn2"): {
			gcnode(ResourceSnapshot, "ns3", "overlay/sn1"),
		},
		gcnode(resourceSnapshotFlat, "ns3", "overlay/sn1"): nil,
		gcnode(resourceSnapshotFlat, "ns3", "overlay/sn3"): nil,
		gcnode(ResourceSnapshot, "ns3", "overlay/sn3"): {
			gcnode(ResourceSnapshot, "ns3", "btrfs/sn1"),
			gcnode(ResourceSnapshot, "ns3", "overlay/sn1"),
		},
		gcnode(resourceImageFlat, "ns1", "image1"): {
			gcnode(resourceContentFlat, "ns1", dgst(3).String()),
		},
		gcnode(resourceImageFlat, "ns1", "image2"): {
			gcnode(resourceContentFlat, "ns1", dgst(4).String()),
		},
	}

	if err := db.Update(func(tx *bolt.Tx) error {
		v1bkt, err := tx.CreateBucketIfNotExists(bucketKeyVersion)
		if err != nil {
			return err
		}
		for _, alter := range alters {
			if err := alter(v1bkt); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("Update failed: %+v", err)
	}

	ctx := logtest.WithT(context.Background(), t)
	c := startGCContext(ctx, nil)
	if err := db.View(func(tx *bolt.Tx) error {
		rctx, rcancel := context.WithCancel(ctx)
		// Use canceled context to avoid blocking on nil chan
		rcancel()
		if err := c.scanRoots(rctx, tx, nil); err != nil && !errors.Is(err, context.Canceled) {
			return err
		}
		return nil
	}); err != nil {
		t.Fatalf("Scan roots failed: %+v", err)
	}

	for n, nodes := range refs {
		checkNodeC(ctx, t, db, nodes, func(ctx context.Context, tx *bolt.Tx, nc chan<- gc.Node) error {
			return c.references(ctx, tx, n, func(n gc.Node) {
				select {
				case nc <- n:
				case <-ctx.Done():
				}
			})
		})
	}
}

func TestCollectibleResources(t *testing.T) {
	db, err := newDatabase(t)
	require.NoError(t, err)

	testResource := gc.ResourceType(0x10)

	alters := []alterFunc{
		addContent("ns1", dgst(1), nil),
		addImage("ns1", "image1", dgst(1), nil),
		addContent("ns1", dgst(2), map[string]string{
			"containerd.io/gc.ref.test": "test2",
		}),
		addImage("ns1", "image2", dgst(2), nil),
		addLease("ns1", "lease1", labelmap(string(labelGCExpire), time.Now().Add(time.Hour).Format(time.RFC3339))),
		addLease("ns1", "lease2", labelmap(string(labelGCExpire), time.Now().Add(-1*time.Hour).Format(time.RFC3339))),
		addContainer("ns1", "container1", "testsn", "sn1", nil),
	}
	refs := map[gc.Node][]gc.Node{
		gcnode(ResourceContent, "ns1", dgst(1).String()): nil,
		gcnode(ResourceContent, "ns1", dgst(2).String()): {
			gcnode(testResource, "ns1", "test2"),
		},
		gcnode(ResourceImage, "ns1", "image1"): {
			gcnode(ResourceContent, "ns1", dgst(1).String()),
		},
		gcnode(ResourceImage, "ns1", "image2"): {
			gcnode(ResourceContent, "ns1", dgst(2).String()),
			gcnode(testResource, "ns1", "test5"),
		},
		gcnode(ResourceContainer, "ns1", "container1"): {
			gcnode(ResourceSnapshot, "ns1", "testsn/sn1"),
			gcnode(testResource, "ns1", "test5"),
		},
	}
	all := []gc.Node{
		gcnode(ResourceContent, "ns1", dgst(1).String()),
		gcnode(ResourceContent, "ns1", dgst(2).String()),
		gcnode(ResourceImage, "ns1", "image1"),
		gcnode(ResourceImage, "ns1", "image2"),
		gcnode(ResourceLease, "ns1", "lease1"),
		gcnode(ResourceLease, "ns1", "lease2"),
		gcnode(testResource, "ns1", "test1"),
		gcnode(testResource, "ns1", "test2"), // 7: Will be removed
		gcnode(testResource, "ns1", "test3"),
		gcnode(testResource, "ns1", "test4"),
		gcnode(testResource, "ns1", "test5"),
	}
	removeIndex := 7
	roots := []gc.Node{
		gcnode(ResourceContainer, "ns1", "container1"),
		gcnode(ResourceImage, "ns1", "image1"),
		gcnode(ResourceImage, "ns1", "image2"),
		gcnode(ResourceLease, "ns1", "lease1"),
		gcnode(testResource, "ns1", "test1"),
		gcnode(testResource, "ns1", "test3"),
	}
	collector := &testCollector{
		all: []gc.Node{
			gcnode(testResource, "ns1", "test1"),
			gcnode(testResource, "ns1", "test2"),
			gcnode(testResource, "ns1", "test3"),
			gcnode(testResource, "ns1", "test4"),
			gcnode(testResource, "ns1", "test5"),
		},
		active: []gc.Node{
			gcnode(testResource, "ns1", "test1"),
		},
		brefs: map[gc.Node]gc.Node{
			gcnode(ResourceImage, "ns1", "image2"):         gcnode(testResource, "ns1", "test5"),
			gcnode(ResourceContainer, "ns1", "container1"): gcnode(testResource, "ns1", "test5"),
		},
		leased: map[string][]gc.Node{
			"lease1": {
				gcnode(testResource, "ns1", "test3"),
			},
			"lease2": {
				gcnode(testResource, "ns1", "test4"),
			},
		},
	}

	if err := db.Update(func(tx *bolt.Tx) error {
		v1bkt, err := tx.CreateBucketIfNotExists(bucketKeyVersion)
		if err != nil {
			return err
		}
		for _, alter := range alters {
			if err := alter(v1bkt); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("Update failed: %+v", err)
	}

	ctx := context.Background()
	c := startGCContext(ctx, map[gc.ResourceType]Collector{
		testResource: collector,
	})

	checkNodeC(ctx, t, db, roots, func(ctx context.Context, tx *bolt.Tx, nc chan<- gc.Node) error {
		return c.scanRoots(ctx, tx, nc)
	})
	for n, nodes := range refs {
		checkNodeC(ctx, t, db, nodes, func(ctx context.Context, tx *bolt.Tx, nc chan<- gc.Node) error {
			return c.references(ctx, tx, n, func(n gc.Node) {
				select {
				case nc <- n:
				case <-ctx.Done():
				}
			})
		})
		if t.Failed() {
			t.Fatalf("Failure scanning %v", n)
		}
	}
	checkNodes(ctx, t, db, all, func(ctx context.Context, tx *bolt.Tx, fn func(context.Context, gc.Node) error) error {
		return c.scanAll(ctx, tx, fn)
	})

	if err := db.Update(func(tx *bolt.Tx) error {
		if _, err := c.remove(ctx, tx, all[removeIndex]); err != nil {
			return err
		}
		return nil
	}); err != nil {
		t.Fatalf("Update failed: %+v", err)
	}
	all = append(all[:removeIndex], all[removeIndex+1:]...)
	checkNodes(ctx, t, db, all, func(ctx context.Context, tx *bolt.Tx, fn func(context.Context, gc.Node) error) error {
		return c.scanAll(ctx, tx, fn)
	})
}

type testCollector struct {
	all    []gc.Node
	active []gc.Node
	brefs  map[gc.Node]gc.Node
	leased map[string][]gc.Node
}

func (tc *testCollector) StartCollection(context.Context) (CollectionContext, error) {
	return tc, nil
}

func (tc *testCollector) ReferenceLabel() string {
	return "test"
}

func (tc *testCollector) All(fn func(gc.Node)) {
	for _, n := range tc.all {
		fn(n)
	}
}
func (tc *testCollector) ActiveWithBackRefs(namespace string, fn func(gc.Node), bref func(gc.Node, gc.Node)) {
	for _, n := range tc.active {
		if n.Namespace == namespace {
			fn(n)
		}
	}
	for b, r := range tc.brefs {
		if b.Namespace == namespace {
			bref(b, r)
		}
	}
}

func (tc *testCollector) Active(namespace string, fn func(gc.Node)) {
	tc.ActiveWithBackRefs(namespace, fn, func(gc.Node, gc.Node) {})
}

func (tc *testCollector) Leased(namespace, lease string, fn func(gc.Node)) {
	for _, n := range tc.leased[lease] {
		if n.Namespace == namespace {
			fn(n)
		}
	}
}

func (tc *testCollector) Remove(n gc.Node) {
	for i := range tc.all {
		if tc.all[i] == n {
			tc.all = append(tc.all[:i], tc.all[i+1:]...)
			return
		}
	}
}

func (tc *testCollector) Cancel() error {
	return nil
}

func (tc *testCollector) Finish() error {
	return nil
}

func newDatabase(t testing.TB) (*bolt.DB, error) {
	td := t.TempDir()

	db, err := bolt.Open(filepath.Join(td, "test.db"), 0777, nil)
	if err != nil {
		return nil, err
	}

	t.Cleanup(func() {
		assert.NoError(t, db.Close())
	})

	return db, nil
}

func checkNodeC(ctx context.Context, t *testing.T, db *bolt.DB, expected []gc.Node, fn func(context.Context, *bolt.Tx, chan<- gc.Node) error) {
	t.Helper()
	var actual []gc.Node
	nc := make(chan gc.Node)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for n := range nc {
			actual = append(actual, n)
		}
	}()
	if err := db.View(func(tx *bolt.Tx) error {
		defer close(nc)
		return fn(ctx, tx, nc)
	}); err != nil {
		t.Fatal(err)
	}

	<-done
	checkNodesEqual(t, actual, expected)
}

func checkNodes(ctx context.Context, t *testing.T, db *bolt.DB, expected []gc.Node, fn func(context.Context, *bolt.Tx, func(context.Context, gc.Node) error) error) {
	t.Helper()
	var actual []gc.Node
	scanFn := func(ctx context.Context, n gc.Node) error {
		actual = append(actual, n)
		return nil
	}

	if err := db.View(func(tx *bolt.Tx) error {
		return fn(ctx, tx, scanFn)
	}); err != nil {
		t.Fatal(err)
	}

	checkNodesEqual(t, actual, expected)
}

func checkNodesEqual(t *testing.T, n1, n2 []gc.Node) {
	t.Helper()
	sort.Sort(nodeList(n1))
	sort.Sort(nodeList(n2))

	if len(n1) != len(n2) {
		buf := bytes.NewBuffer(nil)
		tw := tabwriter.NewWriter(buf, 8, 4, 1, ' ', 0)
		max := len(n1)
		if len(n2) > max {
			max = len(n2)
		}
		fmt.Fprintln(tw, "Expected:\tActual:")
		for i := 0; i < max; i++ {
			var left, right string
			if i < len(n1) {
				right = printNode(n1[i])
			}
			if i < len(n2) {
				left = printNode(n2[i])
			}
			fmt.Fprintln(tw, left+"\t"+right)
		}
		tw.Flush()
		t.Fatal("Nodes do not match\n" + buf.String())
	}

	for i := range n1 {
		if n1[i] != n2[i] {
			t.Errorf("[%d] root does not match expected: expected %v, got %v", i, printNode(n2[i]), printNode(n1[i]))
		}
	}
}

func printNode(n gc.Node) string {
	var t string
	switch n.Type {
	case ResourceContent:
		t = "content"
	case ResourceSnapshot:
		t = "snapshot"
	case ResourceContainer:
		t = "container"
	case ResourceTask:
		t = "task"
	case ResourceImage:
		t = "image"
	case ResourceLease:
		t = "lease"
	case ResourceIngest:
		t = "ingest"
	case resourceContentFlat:
		t = "content-flat"
	case resourceSnapshotFlat:
		t = "snapshot-flat"
	case resourceImageFlat:
		t = "image-flat"
	default:
		return fmt.Sprintf("%v", n)
	}
	return fmt.Sprintf("%s(%s/%s)", t, n.Namespace, n.Key)
}

type nodeList []gc.Node

func (nodes nodeList) Len() int {
	return len(nodes)
}

func (nodes nodeList) Less(i, j int) bool {
	if nodes[i].Type != nodes[j].Type {
		return nodes[i].Type < nodes[j].Type
	}
	if nodes[i].Namespace != nodes[j].Namespace {
		return nodes[i].Namespace < nodes[j].Namespace
	}
	return nodes[i].Key < nodes[j].Key
}

func (nodes nodeList) Swap(i, j int) {
	nodes[i], nodes[j] = nodes[j], nodes[i]
}

type alterFunc func(bkt *bolt.Bucket) error

func addImage(ns, name string, dgst digest.Digest, labels map[string]string) alterFunc {
	return func(bkt *bolt.Bucket) error {
		ibkt, err := createBuckets(bkt, ns, string(bucketKeyObjectImages), name)
		if err != nil {
			return err
		}

		tbkt, err := ibkt.CreateBucket(bucketKeyTarget)
		if err != nil {
			return err
		}
		if err := tbkt.Put(bucketKeyDigest, []byte(dgst.String())); err != nil {
			return err
		}

		return boltutil.WriteLabels(ibkt, labels)
	}
}

func addSnapshot(ns, snapshotter, name, parent string, labels map[string]string) alterFunc {
	return func(bkt *bolt.Bucket) error {
		sbkt, err := createBuckets(bkt, ns, string(bucketKeyObjectSnapshots), snapshotter, name)
		if err != nil {
			return err
		}
		if parent != "" {
			if err := sbkt.Put(bucketKeyParent, []byte(parent)); err != nil {
				return err
			}
		}
		return boltutil.WriteLabels(sbkt, labels)
	}
}

func addContent(ns string, dgst digest.Digest, labels map[string]string) alterFunc {
	return func(bkt *bolt.Bucket) error {
		cbkt, err := createBuckets(bkt, ns, string(bucketKeyObjectContent), string(bucketKeyObjectBlob), dgst.String())
		if err != nil {
			return err
		}
		return boltutil.WriteLabels(cbkt, labels)
	}
}

func addIngest(ns, ref string, expected digest.Digest, expires *time.Time) alterFunc {
	return func(bkt *bolt.Bucket) error {
		cbkt, err := createBuckets(bkt, ns, string(bucketKeyObjectContent), string(bucketKeyObjectIngests), ref)
		if err != nil {
			return err
		}
		if expected != "" {
			if err := cbkt.Put(bucketKeyExpected, []byte(expected)); err != nil {
				return err
			}
		}
		if expires != nil {
			if err := writeExpireAt(*expires, cbkt); err != nil {
				return err
			}
		}
		return nil
	}
}

func addLease(ns, lid string, labels map[string]string) alterFunc {
	return func(bkt *bolt.Bucket) error {
		lbkt, err := createBuckets(bkt, ns, string(bucketKeyObjectLeases), lid)
		if err != nil {
			return err
		}
		return boltutil.WriteLabels(lbkt, labels)
	}
}

func addLeaseSnapshot(ns, lid, snapshotter, name string) alterFunc {
	return func(bkt *bolt.Bucket) error {
		sbkt, err := createBuckets(bkt, ns, string(bucketKeyObjectLeases), lid, string(bucketKeyObjectSnapshots), snapshotter)
		if err != nil {
			return err
		}
		return sbkt.Put([]byte(name), nil)
	}
}

func addLeaseContent(ns, lid string, dgst digest.Digest) alterFunc {
	return func(bkt *bolt.Bucket) error {
		cbkt, err := createBuckets(bkt, ns, string(bucketKeyObjectLeases), lid, string(bucketKeyObjectContent))
		if err != nil {
			return err
		}
		return cbkt.Put([]byte(dgst.String()), nil)
	}
}

func addLeaseImage(ns, lid, image string) alterFunc {
	return func(bkt *bolt.Bucket) error {
		cbkt, err := createBuckets(bkt, ns, string(bucketKeyObjectLeases), lid, string(bucketKeyObjectImages))
		if err != nil {
			return err
		}
		return cbkt.Put([]byte(image), nil)
	}
}

func addLeaseIngest(ns, lid, ref string) alterFunc {
	return func(bkt *bolt.Bucket) error {
		cbkt, err := createBuckets(bkt, ns, string(bucketKeyObjectLeases), lid, string(bucketKeyObjectIngests))
		if err != nil {
			return err
		}
		return cbkt.Put([]byte(ref), nil)
	}
}

func addContainer(ns, name, snapshotter, snapshot string, labels map[string]string) alterFunc {
	return func(bkt *bolt.Bucket) error {
		cbkt, err := createBuckets(bkt, ns, string(bucketKeyObjectContainers), name)
		if err != nil {
			return err
		}
		if err := cbkt.Put(bucketKeySnapshotter, []byte(snapshotter)); err != nil {
			return err
		}
		if err := cbkt.Put(bucketKeySnapshotKey, []byte(snapshot)); err != nil {
			return err
		}
		return boltutil.WriteLabels(cbkt, labels)
	}
}

func addSandbox(ns, name string, labels map[string]string) alterFunc {
	return func(bkt *bolt.Bucket) error {
		sbkt, err := createBuckets(bkt, ns, string(bucketKeyObjectSandboxes), name)
		if err != nil {
			return err
		}
		return boltutil.WriteLabels(sbkt, labels)
	}
}

func createBuckets(bkt *bolt.Bucket, names ...string) (*bolt.Bucket, error) {
	for _, name := range names {
		nbkt, err := bkt.CreateBucketIfNotExists([]byte(name))
		if err != nil {
			return nil, err
		}
		bkt = nbkt
	}
	return bkt, nil
}

func labelmap(kv ...string) map[string]string {
	if len(kv)%2 != 0 {
		panic("bad labels argument")
	}
	l := map[string]string{}
	for i := 0; i < len(kv); i = i + 2 {
		l[kv[i]] = kv[i+1]
	}
	return l
}

func dgst(i int64) digest.Digest {
	r := rand.New(rand.NewSource(i))
	dgstr := digest.SHA256.Digester()
	if _, err := io.CopyN(dgstr.Hash(), r, 256); err != nil {
		panic(err)
	}
	d := dgstr.Digest()
	return digest.NewDigestFromEncoded(d.Algorithm(), fmt.Sprintf("%03d00", i)+d.Encoded()[5:])
}

func timeIn(d time.Duration) *time.Time {
	t := time.Now().UTC().Add(d)
	return &t
}
