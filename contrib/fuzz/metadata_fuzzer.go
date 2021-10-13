//go:build gofuzz
// +build gofuzz

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

	"github.com/containerd/containerd/containers"
	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/content/local"
	"github.com/containerd/containerd/images"
	"github.com/containerd/containerd/leases"
	"github.com/containerd/containerd/metadata"
	"github.com/containerd/containerd/namespaces"
	"github.com/containerd/containerd/snapshots"
	"github.com/containerd/containerd/snapshots/native"
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
		if opType%1 == 0 {
			i := images.Image{}
			err := f.GenerateStruct(&i)
			if err != nil {
				return 0
			}
			_, _ = store.Create(ctx, i)
		} else if opType%2 == 0 {
			newFs, err := f.GetString()
			if err != nil {
				return 0
			}
			_, _ = store.List(ctx, newFs)
		} else if opType%3 == 0 {
			i := images.Image{}
			err := f.GenerateStruct(&i)
			if err != nil {
				return 0
			}
			_, _ = store.Update(ctx, i)
		} else if opType%4 == 0 {
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
		if opType%1 == 0 {
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
		} else if opType%2 == 0 {
			_, _ = lm.List(ctx)
		} else if opType%3 == 0 {
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
		} else if opType%4 == 0 {
			l := leases.Lease{}
			err = f.GenerateStruct(&l)
			if err != nil {
				return 0
			}
			_ = lm.Delete(ctx, l)
		} else if opType%5 == 0 {
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
		} else if opType%6 == 0 {
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
		if opType%1 == 0 {
			err := f.GenerateStruct(&c)
			if err != nil {
				return 0
			}
			db.Update(func(tx *bolt.Tx) error {
				_, _ = store.Create(metadata.WithTransactionContext(ctx, tx), c)
				return nil
			})
		} else if opType%2 == 0 {
			filt, err := f.GetString()
			if err != nil {
				return 0
			}
			_, _ = store.List(ctx, filt)
		} else if opType%3 == 0 {
			id, err := f.GetString()
			if err != nil {
				return 0
			}
			_ = store.Delete(ctx, id)
		} else if opType%4 == 0 {
			fieldpaths, err := f.GetString()
			if err != nil {
				return 0
			}
			_, _ = store.Update(ctx, c, fieldpaths)
		} else if opType%5 == 0 {
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
		if opType%1 == 0 {
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
		} else if opType%2 == 0 {
			info := content.Info{}
			err = f.GenerateStruct(&info)
			if err != nil {
				return 0
			}
			_, _ = cs.Update(ctx, info)
		} else if opType%3 == 0 {
			walkFn := func(info content.Info) error {
				return nil
			}
			_ = cs.Walk(ctx, walkFn)
		} else if opType%4 == 0 {
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
		} else if opType%5 == 0 {
			_, _ = cs.ListStatuses(ctx)
		} else if opType%6 == 0 {
			ref, err := f.GetString()
			if err != nil {
				return 0
			}
			_, _ = cs.Status(ctx, ref)
		} else if opType%7 == 0 {
			ref, err := f.GetString()
			if err != nil {
				return 0
			}
			_ = cs.Abort(ctx, ref)
		} else if opType%8 == 0 {
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
