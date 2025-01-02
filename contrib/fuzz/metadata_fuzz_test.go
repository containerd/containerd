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

package fuzz

import (
	"context"
	"path/filepath"
	"testing"

	fuzz "github.com/AdaLogics/go-fuzz-headers"
	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	bolt "go.etcd.io/bbolt"

	"github.com/containerd/containerd/v2/core/containers"
	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/core/leases"
	"github.com/containerd/containerd/v2/core/metadata"
	"github.com/containerd/containerd/v2/core/snapshots"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/containerd/containerd/v2/plugins/content/local"
	"github.com/containerd/containerd/v2/plugins/snapshots/native"
)

func testEnv(t *testing.T) (context.Context, *bolt.DB, func(), error) {
	dirname := t.TempDir()
	db, err := bolt.Open(filepath.Join(dirname, "meta.db"), 0644, nil)
	if err != nil {
		return nil, nil, nil, err
	}

	ctx, cancel := context.WithCancel(context.Background())
	ctx = namespaces.WithNamespace(ctx, "testing")
	return ctx, db, func() {
		db.Close()
		cancel()
	}, nil
}

func FuzzImageStore(f *testing.F) {
	f.Fuzz(func(t *testing.T, data []byte) {
		const (
			opCreate = "Create"
			opList   = "List"
			opUpdate = "Update"
			opDelete = "Delete"
		)
		imageStoreOptions := []string{opCreate, opList, opUpdate, opDelete}

		ctx, db, cancel, err := testEnv(t)
		if err != nil {
			return
		}
		defer cancel()
		store := metadata.NewImageStore(metadata.NewDB(db, nil, nil))
		f := fuzz.NewConsumer(data)
		noOfOperations, err := f.GetInt()
		if err != nil {
			return
		}
		maxOperations := 50
		for i := 0; i < noOfOperations%maxOperations; i++ {
			opType, err := f.GetInt()
			if err != nil {
				return
			}
			switch imageStoreOptions[opType%len(imageStoreOptions)] {
			case opCreate:
				i := images.Image{}
				err := f.GenerateStruct(&i)
				if err != nil {
					return
				}
				_, _ = store.Create(ctx, i)
			case opList:
				newFs, err := f.GetString()
				if err != nil {
					return
				}
				_, _ = store.List(ctx, newFs)
			case opUpdate:
				i := images.Image{}
				err := f.GenerateStruct(&i)
				if err != nil {
					return
				}
				_, _ = store.Update(ctx, i)
			case opDelete:
				name, err := f.GetString()
				if err != nil {
					return
				}
				_ = store.Delete(ctx, name)
			}
		}
	})
}

func FuzzLeaseManager(f *testing.F) {
	f.Fuzz(func(t *testing.T, data []byte) {
		const (
			opCreate         = "Create"
			opList           = "List"
			opAddResource    = "AddResource"
			opDelete         = "Delete"
			opDeleteResource = "DeleteResource"
			opListResources  = "ListResources"
		)
		leaseManagerOptions := []string{opCreate, opList, opAddResource, opDelete, opDeleteResource, opListResources}
		ctx, db, cancel, err := testEnv(t)
		if err != nil {
			return
		}
		defer cancel()
		lm := metadata.NewLeaseManager(metadata.NewDB(db, nil, nil))

		f := fuzz.NewConsumer(data)
		noOfOperations, err := f.GetInt()
		if err != nil {
			return
		}
		maxOperations := 50
		for i := 0; i < noOfOperations%maxOperations; i++ {
			opType, err := f.GetInt()
			if err != nil {
				return
			}
			switch leaseManagerOptions[opType%len(leaseManagerOptions)] {
			case opCreate:
				err := db.Update(func(tx *bolt.Tx) error {
					sm := make(map[string]string)
					err2 := f.FuzzMap(&sm)
					if err2 != nil {
						return err2
					}
					_, _ = lm.Create(ctx, leases.WithLabels(sm))
					return nil
				})
				if err != nil {
					return
				}
			case opList:
				_, _ = lm.List(ctx)
			case opAddResource:
				l := leases.Lease{}
				err := f.GenerateStruct(&l)
				if err != nil {
					return
				}
				r := leases.Resource{}
				err = f.GenerateStruct(&r)
				if err != nil {
					return
				}
				db.Update(func(tx *bolt.Tx) error {
					_ = lm.AddResource(metadata.WithTransactionContext(ctx, tx), l, r)
					return nil
				})
			case opDelete:
				l := leases.Lease{}
				err = f.GenerateStruct(&l)
				if err != nil {
					return
				}
				_ = lm.Delete(ctx, l)
			case opDeleteResource:
				l := leases.Lease{}
				err := f.GenerateStruct(&l)
				if err != nil {
					return
				}
				r := leases.Resource{}
				err = f.GenerateStruct(&r)
				if err != nil {
					return
				}
				_ = lm.DeleteResource(ctx, l, r)
			case opListResources:
				l := leases.Lease{}
				err := f.GenerateStruct(&l)
				if err != nil {
					return
				}
				_, _ = lm.ListResources(ctx, l)
			}
		}
	})
}

func FuzzContainerStore(f *testing.F) {
	f.Fuzz(func(t *testing.T, data []byte) {
		const (
			opCreate = "Create"
			opList   = "List"
			opDelete = "Delete"
			opUpdate = "Update"
			opGet    = "Get"
		)
		containerStoreOptions := []string{opCreate, opList, opDelete, opUpdate, opGet}
		ctx, db, cancel, err := testEnv(t)
		if err != nil {
			return
		}
		defer cancel()

		store := metadata.NewContainerStore(metadata.NewDB(db, nil, nil))
		c := containers.Container{}
		f := fuzz.NewConsumer(data)
		noOfOperations, err := f.GetInt()
		if err != nil {
			return
		}
		maxOperations := 50
		for i := 0; i < noOfOperations%maxOperations; i++ {
			opType, err := f.GetInt()
			if err != nil {
				return
			}
			switch containerStoreOptions[opType%len(containerStoreOptions)] {
			case opCreate:
				err := f.GenerateStruct(&c)
				if err != nil {
					return
				}
				db.Update(func(tx *bolt.Tx) error {
					_, _ = store.Create(metadata.WithTransactionContext(ctx, tx), c)
					return nil
				})
			case opList:
				filt, err := f.GetString()
				if err != nil {
					return
				}
				_, _ = store.List(ctx, filt)
			case opDelete:
				id, err := f.GetString()
				if err != nil {
					return
				}
				_ = store.Delete(ctx, id)
			case opUpdate:
				fieldpaths, err := f.GetString()
				if err != nil {
					return
				}
				_, _ = store.Update(ctx, c, fieldpaths)
			case opGet:
				id, err := f.GetString()
				if err != nil {
					return
				}
				_, _ = store.Get(ctx, id)
			}
		}
	})
}

type testOptions struct {
	extraSnapshots map[string]func(string) (snapshots.Snapshotter, error)
}

type testOpt func(*testOptions)

func testDB(t *testing.T, opt ...testOpt) (context.Context, *metadata.DB, func(), error) {
	ctx, cancel := context.WithCancel(context.Background())
	ctx = namespaces.WithNamespace(ctx, "testing")

	var topts testOptions

	for _, o := range opt {
		o(&topts)
	}

	dirname := t.TempDir()

	snapshotter, err := native.NewSnapshotter(filepath.Join(dirname, "native"))
	if err != nil {
		return ctx, nil, func() { cancel() }, err
	}

	snapshotters := map[string]snapshots.Snapshotter{
		"native": snapshotter,
	}

	for name, fn := range topts.extraSnapshots {
		snapshotter, err := fn(filepath.Join(dirname, name))
		if err != nil {
			return ctx, nil, func() { cancel() }, err
		}
		snapshotters[name] = snapshotter
	}

	cs, err := local.NewStore(filepath.Join(dirname, "content"))
	if err != nil {
		return ctx, nil, func() { cancel() }, err
	}

	bdb, err := bolt.Open(filepath.Join(dirname, "metadata.db"), 0644, nil)
	if err != nil {
		return ctx, nil, func() { cancel() }, err
	}

	db := metadata.NewDB(bdb, cs, snapshotters)
	if err := db.Init(ctx); err != nil {
		return ctx, nil, func() { cancel() }, err
	}

	return ctx, db, func() {
		bdb.Close()
		cancel()
	}, nil
}

func FuzzContentStore(f *testing.F) {
	f.Fuzz(func(t *testing.T, data []byte) {
		const (
			opInfo         = "Info"
			opUpdate       = "Update"
			opWalk         = "Walk"
			opDelete       = "Delete"
			opListStatuses = "ListStatuses"
			opStatus       = "Status"
			opAbort        = "Abort"
			opCommit       = "Commit"
		)
		contentStoreOptions := []string{opInfo, opUpdate, opWalk, opDelete, opListStatuses, opStatus, opAbort, opCommit}
		ctx, db, cancel, err := testDB(t)
		defer cancel()
		if err != nil {
			return
		}

		cs := db.ContentStore()
		f := fuzz.NewConsumer(data)
		noOfOperations, err := f.GetInt()
		if err != nil {
			return
		}
		maxOperations := 50
		for i := 0; i < noOfOperations%maxOperations; i++ {
			opType, err := f.GetInt()
			if err != nil {
				return
			}
			switch contentStoreOptions[opType%len(contentStoreOptions)] {
			case opInfo:
				blob, err := f.GetBytes()
				if err != nil {
					return
				}
				dgst := digest.FromBytes(blob)
				err = dgst.Validate()
				if err != nil {
					return
				}
				_, _ = cs.Info(ctx, dgst)
			case opUpdate:
				info := content.Info{}
				err = f.GenerateStruct(&info)
				if err != nil {
					return
				}
				_, _ = cs.Update(ctx, info)
			case opWalk:
				walkFn := func(info content.Info) error {
					return nil
				}
				_ = cs.Walk(ctx, walkFn)
			case opDelete:
				blob, err := f.GetBytes()
				if err != nil {
					return
				}
				dgst := digest.FromBytes(blob)
				err = dgst.Validate()
				if err != nil {
					return
				}
				_ = cs.Delete(ctx, dgst)
			case opListStatuses:
				_, _ = cs.ListStatuses(ctx)
			case opStatus:
				ref, err := f.GetString()
				if err != nil {
					return
				}
				_, _ = cs.Status(ctx, ref)
			case opAbort:
				ref, err := f.GetString()
				if err != nil {
					return
				}
				_ = cs.Abort(ctx, ref)
			case opCommit:
				desc := ocispec.Descriptor{}
				err = f.GenerateStruct(&desc)
				if err != nil {
					return
				}
				ref, err := f.GetString()
				if err != nil {
					return
				}
				csWriter, err := cs.Writer(ctx,
					content.WithDescriptor(desc),
					content.WithRef(ref))
				if err != nil {
					return
				}
				defer csWriter.Close()
				p, err := f.GetBytes()
				if err != nil {
					return
				}
				_, _ = csWriter.Write(p)
				_ = csWriter.Commit(ctx, 0, csWriter.Digest())
			}
		}
	})
}
