//go:build gofuzz

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
	"fmt"
	"os"
	"path/filepath"

	fuzz "github.com/AdaLogics/go-fuzz-headers"
	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	bolt "go.etcd.io/bbolt"

	"github.com/containerd/containerd/v2/containers"
	"github.com/containerd/containerd/v2/content"
	"github.com/containerd/containerd/v2/content/local"
	"github.com/containerd/containerd/v2/images"
	"github.com/containerd/containerd/v2/leases"
	"github.com/containerd/containerd/v2/metadata"
	"github.com/containerd/containerd/v2/namespaces"
	"github.com/containerd/containerd/v2/snapshots"
	"github.com/containerd/containerd/v2/snapshots/native"
)

func testEnv() (context.Context, *bolt.DB, func(), error) {
	ctx, cancel := context.WithCancel(context.Background())
	ctx = namespaces.WithNamespace(ctx, "testing")

	dirname, err := os.MkdirTemp("", "fuzz-")
	if err != nil {
		return ctx, nil, nil, err
	}

	db, err := bolt.Open(filepath.Join(dirname, "meta.db"), 0644, nil)
	if err != nil {
		return ctx, nil, nil, err
	}

	return ctx, db, func() {
		db.Close()
		_ = os.RemoveAll(dirname)
		cancel()
	}, nil
}

func FuzzImageStore(data []byte) int {
	imageStoreOptions := map[int]string{
		0: "Create",
		1: "List",
		2: "Update",
		3: "Delete",
	}

	ctx, db, cancel, err := testEnv()
	if err != nil {
		return 0
	}
	defer cancel()
	store := metadata.NewImageStore(metadata.NewDB(db, nil, nil))
	f := fuzz.NewConsumer(data)
	noOfOperations, err := f.GetInt()
	if err != nil {
		return 0
	}
	maxOperations := 50
	for i := 0; i < noOfOperations%maxOperations; i++ {
		opType, err := f.GetInt()
		if err != nil {
			return 0
		}
		switch imageStoreOptions[opType%len(imageStoreOptions)] {
		case "Create":
			i := images.Image{}
			err := f.GenerateStruct(&i)
			if err != nil {
				return 0
			}
			_, _ = store.Create(ctx, i)
		case "List":
			newFs, err := f.GetString()
			if err != nil {
				return 0
			}
			_, _ = store.List(ctx, newFs)
		case "Update":
			i := images.Image{}
			err := f.GenerateStruct(&i)
			if err != nil {
				return 0
			}
			_, _ = store.Update(ctx, i)
		case "Delete":
			name, err := f.GetString()
			if err != nil {
				return 0
			}
			_ = store.Delete(ctx, name)
		}
	}
	return 1
}

func FuzzLeaseManager(data []byte) int {
	leaseManagerOptions := map[int]string{
		0: "Create",
		1: "List",
		2: "AddResource",
		3: "Delete",
		4: "DeleteResource",
		5: "ListResources",
	}
	ctx, db, cancel, err := testEnv()
	if err != nil {
		return 0
	}
	defer cancel()
	lm := metadata.NewLeaseManager(metadata.NewDB(db, nil, nil))

	f := fuzz.NewConsumer(data)
	noOfOperations, err := f.GetInt()
	if err != nil {
		return 0
	}
	maxOperations := 50
	for i := 0; i < noOfOperations%maxOperations; i++ {
		opType, err := f.GetInt()
		if err != nil {
			return 0
		}
		switch leaseManagerOptions[opType%len(leaseManagerOptions)] {
		case "Create":
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
				return 0
			}
		case "List":
			_, _ = lm.List(ctx)
		case "AddResource":
			l := leases.Lease{}
			err := f.GenerateStruct(&l)
			if err != nil {
				return 0
			}
			r := leases.Resource{}
			err = f.GenerateStruct(&r)
			if err != nil {
				return 0
			}
			db.Update(func(tx *bolt.Tx) error {
				_ = lm.AddResource(metadata.WithTransactionContext(ctx, tx), l, r)
				return nil
			})
		case "Delete":
			l := leases.Lease{}
			err = f.GenerateStruct(&l)
			if err != nil {
				return 0
			}
			_ = lm.Delete(ctx, l)
		case "DeleteResource":
			l := leases.Lease{}
			err := f.GenerateStruct(&l)
			if err != nil {
				return 0
			}
			r := leases.Resource{}
			err = f.GenerateStruct(&r)
			if err != nil {
				return 0
			}
			_ = lm.DeleteResource(ctx, l, r)
		case "ListResources":
			l := leases.Lease{}
			err := f.GenerateStruct(&l)
			if err != nil {
				return 0
			}
			_, _ = lm.ListResources(ctx, l)
		}
	}
	return 1
}

func FuzzContainerStore(data []byte) int {
	containerStoreOptions := map[int]string{
		0: "Create",
		1: "List",
		2: "Delete",
		3: "Update",
		4: "Get",
	}
	ctx, db, cancel, err := testEnv()
	if err != nil {
		return 0
	}
	defer cancel()

	store := metadata.NewContainerStore(metadata.NewDB(db, nil, nil))
	c := containers.Container{}
	f := fuzz.NewConsumer(data)
	noOfOperations, err := f.GetInt()
	if err != nil {
		return 0
	}
	maxOperations := 50
	for i := 0; i < noOfOperations%maxOperations; i++ {
		opType, err := f.GetInt()
		if err != nil {
			return 0
		}
		switch containerStoreOptions[opType%len(containerStoreOptions)] {
		case "Create":
			err := f.GenerateStruct(&c)
			if err != nil {
				return 0
			}
			db.Update(func(tx *bolt.Tx) error {
				_, _ = store.Create(metadata.WithTransactionContext(ctx, tx), c)
				return nil
			})
		case "List":
			filt, err := f.GetString()
			if err != nil {
				return 0
			}
			_, _ = store.List(ctx, filt)
		case "Delete":
			id, err := f.GetString()
			if err != nil {
				return 0
			}
			_ = store.Delete(ctx, id)
		case "Update":
			fieldpaths, err := f.GetString()
			if err != nil {
				return 0
			}
			_, _ = store.Update(ctx, c, fieldpaths)
		case "Get":
			id, err := f.GetString()
			if err != nil {
				return 0
			}
			_, _ = store.Get(ctx, id)
		}
	}
	return 1
}

type testOptions struct {
	extraSnapshots map[string]func(string) (snapshots.Snapshotter, error)
}

type testOpt func(*testOptions)

func testDB(opt ...testOpt) (context.Context, *metadata.DB, func(), error) {
	ctx, cancel := context.WithCancel(context.Background())
	ctx = namespaces.WithNamespace(ctx, "testing")

	var topts testOptions

	for _, o := range opt {
		o(&topts)
	}

	dirname, err := os.MkdirTemp("", "fuzzing-")
	if err != nil {
		return ctx, nil, func() { cancel() }, err
	}
	defer os.RemoveAll(dirname)

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
		if err := os.RemoveAll(dirname); err != nil {
			fmt.Println("Failed removing temp dir")
		}
		cancel()
	}, nil
}

func FuzzContentStore(data []byte) int {
	contentStoreOptions := map[int]string{
		0: "Info",
		1: "Update",
		2: "Walk",
		3: "Delete",
		4: "ListStatuses",
		5: "Status",
		6: "Abort",
		7: "Commit",
	}
	ctx, db, cancel, err := testDB()
	defer cancel()
	if err != nil {
		return 0
	}

	cs := db.ContentStore()
	f := fuzz.NewConsumer(data)
	noOfOperations, err := f.GetInt()
	if err != nil {
		return 0
	}
	maxOperations := 50
	for i := 0; i < noOfOperations%maxOperations; i++ {
		opType, err := f.GetInt()
		if err != nil {
			return 0
		}
		switch contentStoreOptions[opType%len(contentStoreOptions)] {
		case "Info":
			blob, err := f.GetBytes()
			if err != nil {
				return 0
			}
			dgst := digest.FromBytes(blob)
			err = dgst.Validate()
			if err != nil {
				return 0
			}
			_, _ = cs.Info(ctx, dgst)
		case "Update":
			info := content.Info{}
			err = f.GenerateStruct(&info)
			if err != nil {
				return 0
			}
			_, _ = cs.Update(ctx, info)
		case "Walk":
			walkFn := func(info content.Info) error {
				return nil
			}
			_ = cs.Walk(ctx, walkFn)
		case "Delete":
			blob, err := f.GetBytes()
			if err != nil {
				return 0
			}
			dgst := digest.FromBytes(blob)
			err = dgst.Validate()
			if err != nil {
				return 0
			}
			_ = cs.Delete(ctx, dgst)
		case "ListStatuses":
			_, _ = cs.ListStatuses(ctx)
		case "Status":
			ref, err := f.GetString()
			if err != nil {
				return 0
			}
			_, _ = cs.Status(ctx, ref)
		case "Abort":
			ref, err := f.GetString()
			if err != nil {
				return 0
			}
			_ = cs.Abort(ctx, ref)
		case "Commit":
			desc := ocispec.Descriptor{}
			err = f.GenerateStruct(&desc)
			if err != nil {
				return 0
			}
			ref, err := f.GetString()
			if err != nil {
				return 0
			}
			csWriter, err := cs.Writer(ctx,
				content.WithDescriptor(desc),
				content.WithRef(ref))
			if err != nil {
				return 0
			}
			defer csWriter.Close()
			p, err := f.GetBytes()
			if err != nil {
				return 0
			}
			_, _ = csWriter.Write(p)
			_ = csWriter.Commit(ctx, 0, csWriter.Digest())
		}
	}
	return 1
}
