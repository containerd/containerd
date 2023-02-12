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
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/containerd/containerd/containers"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/filters"
	"github.com/containerd/containerd/log/logtest"
	"github.com/containerd/containerd/namespaces"
	"github.com/containerd/containerd/protobuf"
	"github.com/containerd/containerd/protobuf/types"
	"github.com/containerd/typeurl/v2"
	"github.com/google/go-cmp/cmp"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	bolt "go.etcd.io/bbolt"
)

func init() {
	typeurl.Register(&specs.Spec{}, "types.containerd.io/opencontainers/runtime-spec", "v1", "Spec")
}

func TestContainersList(t *testing.T) {
	ctx, db := testEnv(t)
	store := NewContainerStore(NewDB(db, nil, nil))
	spec := &specs.Spec{}
	encoded, err := protobuf.MarshalAnyToProto(spec)
	require.NoError(t, err)

	testset := map[string]*containers.Container{}
	for i := 0; i < 4; i++ {
		id := "container-" + fmt.Sprint(i)
		testset[id] = &containers.Container{
			ID: id,
			Labels: map[string]string{
				"idlabel": id,
				"even":    fmt.Sprint(i%2 == 0),
				"odd":     fmt.Sprint(i%2 != 0),
			},
			Spec:        encoded,
			SnapshotKey: "test-snapshot-key",
			Snapshotter: "snapshotter",
			Runtime: containers.RuntimeInfo{
				Name: "testruntime",
			},
			Image: "test image",
		}

		if err := db.Update(func(tx *bolt.Tx) error {
			now := time.Now()
			result, err := store.Create(WithTransactionContext(ctx, tx), *testset[id])
			if err != nil {
				return err
			}

			checkContainerTimestamps(t, &result, now, true)
			testset[id].UpdatedAt, testset[id].CreatedAt = result.UpdatedAt, result.CreatedAt
			checkContainersEqual(t, &result, testset[id], "ensure that containers were created as expected for list")
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}

	for _, testcase := range []struct {
		name    string
		filters []string
	}{
		{
			name: "FullSet",
		},
		{
			name:    "FullSetFiltered", // full set, but because we have OR filter
			filters: []string{"labels.even==true", "labels.odd==true"},
		},
		{
			name:    "Even",
			filters: []string{"labels.even==true"},
		},
		{
			name:    "Odd",
			filters: []string{"labels.odd==true"},
		},
		{
			name:    "ByID",
			filters: []string{"id==container-0"},
		},
		{
			name:    "ByIDLabelEven",
			filters: []string{"labels.idlabel==container-0,labels.even==true"},
		},
		{
			name:    "ByRuntime",
			filters: []string{"runtime.name==testruntime"},
		},
	} {
		t.Run(testcase.name, func(t *testing.T) {
			testset := testset
			if len(testcase.filters) > 0 {
				fs, err := filters.ParseAll(testcase.filters...)
				if err != nil {
					t.Fatal(err)
				}

				newtestset := make(map[string]*containers.Container, len(testset))
				for k, v := range testset {
					if fs.Match(adaptContainer(*v)) {
						newtestset[k] = v
					}
				}
				testset = newtestset
			}

			results, err := store.List(ctx, testcase.filters...)
			if err != nil {
				t.Fatal(err)
			}

			if len(results) == 0 { // all tests return a non-empty result set
				t.Fatalf("not results returned")
			}

			if len(results) != len(testset) {
				t.Fatalf("length of result does not match testset: %v != %v", len(results), len(testset))
			}

			for _, result := range results {
				result := result
				checkContainersEqual(t, &result, testset[result.ID], "list results did not match")
			}
		})
	}

	// delete everything to test it
	for id := range testset {
		if err := store.Delete(ctx, id); err != nil {
			t.Fatal(err)
		}

		// try it again, get NotFound
		if err := store.Delete(ctx, id); err == nil {
			t.Fatalf("expected error deleting non-existent container")
		} else if !errdefs.IsNotFound(err) {
			t.Fatalf("unexpected error %v", err)
		}
	}
}

// TestContainersUpdate ensures that updates are taken in an expected manner.
func TestContainersCreateUpdateDelete(t *testing.T) {
	var (
		ctx, db = testEnv(t)
		store   = NewContainerStore(NewDB(db, nil, nil))
		spec    = &specs.Spec{}
	)

	encoded, err := protobuf.MarshalAnyToProto(spec)
	require.NoError(t, err)

	spec.Annotations = map[string]string{"updated": "true"}
	encodedUpdated, err := protobuf.MarshalAnyToProto(spec)
	require.NoError(t, err)

	for _, testcase := range []struct {
		name       string
		original   containers.Container
		createerr  error
		input      containers.Container
		fieldpaths []string
		expected   containers.Container
		cause      error
	}{
		{
			name: "UpdateIDFail",
			original: containers.Container{
				Spec:        encoded,
				SnapshotKey: "test-snapshot-key",
				Snapshotter: "snapshotter",
				Runtime: containers.RuntimeInfo{
					Name: "testruntime",
				},
			},
			input: containers.Container{
				ID:   "newid",
				Spec: encoded,
				Runtime: containers.RuntimeInfo{
					Name: "testruntime",
				},
			},
			fieldpaths: []string{"id"},
			cause:      errdefs.ErrNotFound,
		},
		{
			name: "UpdateRuntimeFail",
			original: containers.Container{
				SnapshotKey: "test-snapshot-key",
				Snapshotter: "snapshotter",
				Spec:        encoded,
				Runtime: containers.RuntimeInfo{
					Name: "testruntime",
				},
			},
			input: containers.Container{
				Spec: encoded,
				Runtime: containers.RuntimeInfo{
					Name: "testruntimedifferent",
				},
			},
			fieldpaths: []string{"runtime"},
			cause:      errdefs.ErrInvalidArgument,
		},
		{
			name: "UpdateRuntimeClearFail",
			original: containers.Container{
				Spec:        encoded,
				SnapshotKey: "test-snapshot-key",
				Snapshotter: "snapshotter",
				Runtime: containers.RuntimeInfo{
					Name: "testruntime",
				},
			},
			input: containers.Container{
				Spec: encoded,
			},
			fieldpaths: []string{"runtime"},
			cause:      errdefs.ErrInvalidArgument,
		},
		{
			name: "UpdateSpec",
			original: containers.Container{
				Spec:        encoded,
				SnapshotKey: "test-snapshot-key",
				Snapshotter: "snapshotter",
				Runtime: containers.RuntimeInfo{
					Name: "testruntime",
				},
				Image: "test image",
			},
			input: containers.Container{
				Spec: encodedUpdated,
			},
			fieldpaths: []string{"spec"},
			expected: containers.Container{
				Runtime: containers.RuntimeInfo{
					Name: "testruntime",
				},
				Spec:        encodedUpdated,
				SnapshotKey: "test-snapshot-key",
				Snapshotter: "snapshotter",
				Image:       "test image",
			},
		},
		{
			name: "UpdateSnapshot",
			original: containers.Container{

				Spec:        encoded,
				SnapshotKey: "test-snapshot-key",
				Snapshotter: "snapshotter",
				Runtime: containers.RuntimeInfo{
					Name: "testruntime",
				},
				Image: "test image",
			},
			input: containers.Container{
				SnapshotKey: "test2-snapshot-key",
			},
			fieldpaths: []string{"snapshotkey"},
			expected: containers.Container{

				Spec:        encoded,
				SnapshotKey: "test2-snapshot-key",
				Snapshotter: "snapshotter",
				Runtime: containers.RuntimeInfo{
					Name: "testruntime",
				},
				Image: "test image",
			},
		},
		{
			name: "UpdateImage",
			original: containers.Container{

				Spec:        encoded,
				SnapshotKey: "test-snapshot-key",
				Snapshotter: "snapshotter",
				Runtime: containers.RuntimeInfo{
					Name: "testruntime",
				},
				Image: "test image",
			},
			input: containers.Container{
				Image: "test2 image",
			},
			fieldpaths: []string{"image"},
			expected: containers.Container{

				Spec:        encoded,
				SnapshotKey: "test-snapshot-key",
				Snapshotter: "snapshotter",
				Runtime: containers.RuntimeInfo{
					Name: "testruntime",
				},
				Image: "test2 image",
			},
		},
		{
			name: "UpdateLabel",
			original: containers.Container{
				Labels: map[string]string{
					"foo": "one",
					"bar": "two",
				},
				Spec:        encoded,
				SnapshotKey: "test-snapshot-key",
				Snapshotter: "snapshotter",
				Runtime: containers.RuntimeInfo{
					Name: "testruntime",
				},
				Image: "test image",
			},
			input: containers.Container{
				Labels: map[string]string{
					"bar": "baz",
				},
			},
			fieldpaths: []string{"labels.bar"},
			expected: containers.Container{
				Labels: map[string]string{
					"foo": "one",
					"bar": "baz",
				},
				Spec:        encoded,
				SnapshotKey: "test-snapshot-key",
				Snapshotter: "snapshotter",
				Runtime: containers.RuntimeInfo{
					Name: "testruntime",
				},
				Image: "test image",
			},
		},
		{
			name: "DeleteAllLabels",
			original: containers.Container{
				Labels: map[string]string{
					"foo": "one",
					"bar": "two",
				},
				Spec:        encoded,
				SnapshotKey: "test-snapshot-key",
				Snapshotter: "snapshotter",
				Runtime: containers.RuntimeInfo{
					Name: "testruntime",
				},
				Image: "test image",
			},
			input: containers.Container{
				Labels: nil,
			},
			fieldpaths: []string{"labels"},
			expected: containers.Container{
				Spec:        encoded,
				SnapshotKey: "test-snapshot-key",
				Snapshotter: "snapshotter",
				Runtime: containers.RuntimeInfo{
					Name: "testruntime",
				},
				Image: "test image",
			},
		},
		{
			name: "DeleteLabel",
			original: containers.Container{
				Labels: map[string]string{
					"foo": "one",
					"bar": "two",
				},
				Spec:        encoded,
				SnapshotKey: "test-snapshot-key",
				Snapshotter: "snapshotter",
				Runtime: containers.RuntimeInfo{
					Name: "testruntime",
				},
				Image: "test image",
			},
			input: containers.Container{
				Labels: map[string]string{
					"bar": "",
				},
			},
			fieldpaths: []string{"labels.bar"},
			expected: containers.Container{
				Labels: map[string]string{
					"foo": "one",
				},
				Spec:        encoded,
				SnapshotKey: "test-snapshot-key",
				Snapshotter: "snapshotter",
				Runtime: containers.RuntimeInfo{
					Name: "testruntime",
				},
				Image: "test image",
			},
		},
		{
			name: "UpdateSnapshotKeyImmutable",
			original: containers.Container{
				Spec:        encoded,
				SnapshotKey: "",
				Snapshotter: "",
				Runtime: containers.RuntimeInfo{
					Name: "testruntime",
				},
			},
			input: containers.Container{
				SnapshotKey: "something",
				Snapshotter: "something",
			},
			fieldpaths: []string{"snapshotkey", "snapshotter"},
			cause:      errdefs.ErrInvalidArgument,
		},
		{
			name: "SnapshotKeyWithoutSnapshot",
			original: containers.Container{
				Spec:        encoded,
				SnapshotKey: "/nosnapshot",
				Snapshotter: "",
				Runtime: containers.RuntimeInfo{
					Name: "testruntime",
				},
			},
			createerr: errdefs.ErrInvalidArgument,
		},
		{
			name: "UpdateExtensionsFull",
			original: containers.Container{
				Spec: encoded,
				Runtime: containers.RuntimeInfo{
					Name: "testruntime",
				},
				Extensions: map[string]typeurl.Any{
					"hello": &types.Any{
						TypeUrl: "test.update.extensions",
						Value:   []byte("hello"),
					},
				},
			},
			input: containers.Container{
				Spec: encoded,
				Runtime: containers.RuntimeInfo{
					Name: "testruntime",
				},
				Extensions: map[string]typeurl.Any{
					"hello": &types.Any{
						TypeUrl: "test.update.extensions",
						Value:   []byte("world"),
					},
				},
			},
			expected: containers.Container{
				Spec: encoded,
				Runtime: containers.RuntimeInfo{
					Name: "testruntime",
				},
				Extensions: map[string]typeurl.Any{
					"hello": &types.Any{
						TypeUrl: "test.update.extensions",
						Value:   []byte("world"),
					},
				},
			},
		},
		{
			name: "UpdateExtensionsNotInFieldpath",
			original: containers.Container{
				Spec: encoded,
				Runtime: containers.RuntimeInfo{
					Name: "testruntime",
				},
				Extensions: map[string]typeurl.Any{
					"hello": &types.Any{
						TypeUrl: "test.update.extensions",
						Value:   []byte("hello"),
					},
				},
			},
			input: containers.Container{
				Spec: encoded,
				Runtime: containers.RuntimeInfo{
					Name: "testruntime",
				},
				Extensions: map[string]typeurl.Any{
					"hello": &types.Any{
						TypeUrl: "test.update.extensions",
						Value:   []byte("world"),
					},
				},
			},
			fieldpaths: []string{"labels"},
			expected: containers.Container{
				Spec: encoded,
				Runtime: containers.RuntimeInfo{
					Name: "testruntime",
				},
				Extensions: map[string]typeurl.Any{
					"hello": &types.Any{
						TypeUrl: "test.update.extensions",
						Value:   []byte("hello"),
					},
				},
			},
		},
		{
			name: "UpdateExtensionsFieldPath",
			original: containers.Container{
				Spec: encoded,
				Runtime: containers.RuntimeInfo{
					Name: "testruntime",
				},
				Extensions: map[string]typeurl.Any{
					"hello": &types.Any{
						TypeUrl: "test.update.extensions",
						Value:   []byte("hello"),
					},
				},
			},
			input: containers.Container{
				Labels: map[string]string{
					"foo": "one",
				},
				Extensions: map[string]typeurl.Any{
					"hello": &types.Any{
						TypeUrl: "test.update.extensions",
						Value:   []byte("world"),
					},
				},
			},
			fieldpaths: []string{"extensions"},
			expected: containers.Container{
				Spec: encoded,
				Runtime: containers.RuntimeInfo{
					Name: "testruntime",
				},
				Extensions: map[string]typeurl.Any{
					"hello": &types.Any{
						TypeUrl: "test.update.extensions",
						Value:   []byte("world"),
					},
				},
			},
		},
		{
			name: "UpdateExtensionsFieldPathIsolated",
			original: containers.Container{
				Spec: encoded,
				Runtime: containers.RuntimeInfo{
					Name: "testruntime",
				},
				Extensions: map[string]typeurl.Any{
					// leaves hello in place.
					"hello": &types.Any{
						TypeUrl: "test.update.extensions",
						Value:   []byte("hello"),
					},
				},
			},
			input: containers.Container{
				Extensions: map[string]typeurl.Any{
					"hello": &types.Any{
						TypeUrl: "test.update.extensions",
						Value:   []byte("universe"), // this will be ignored
					},
					"bar": &types.Any{
						TypeUrl: "test.update.extensions",
						Value:   []byte("foo"), // this will be added
					},
				},
			},
			fieldpaths: []string{"extensions.bar"}, //
			expected: containers.Container{
				Spec: encoded,
				Runtime: containers.RuntimeInfo{
					Name: "testruntime",
				},
				Extensions: map[string]typeurl.Any{
					"hello": &types.Any{
						TypeUrl: "test.update.extensions",
						Value:   []byte("hello"), // remains as world
					},
					"bar": &types.Any{
						TypeUrl: "test.update.extensions",
						Value:   []byte("foo"), // this will be added
					},
				},
			},
		},
	} {
		t.Run(testcase.name, func(t *testing.T) {
			testcase.original.ID = testcase.name
			if testcase.input.ID == "" {
				testcase.input.ID = testcase.name
			}
			testcase.expected.ID = testcase.name

			now := time.Now().UTC()

			result, err := store.Create(ctx, testcase.original)
			if !errors.Is(err, testcase.createerr) {
				if testcase.createerr == nil {
					t.Fatalf("unexpected error: %v", err)
				} else {
					t.Fatalf("cause of %v (cause: %v) != %v", err, errors.Unwrap(err), testcase.createerr)
				}
			} else if testcase.createerr != nil {
				return
			}

			checkContainerTimestamps(t, &result, now, true)

			// ensure that createdat is never tampered with
			testcase.original.CreatedAt = result.CreatedAt
			testcase.expected.CreatedAt = result.CreatedAt
			testcase.original.UpdatedAt = result.UpdatedAt
			testcase.expected.UpdatedAt = result.UpdatedAt

			checkContainersEqual(t, &result, &testcase.original, "unexpected result on container update")

			now = time.Now()
			result, err = store.Update(ctx, testcase.input, testcase.fieldpaths...)
			if !errors.Is(err, testcase.cause) {
				if testcase.cause == nil {
					t.Fatalf("unexpected error: %v", err)
				} else {
					t.Fatalf("cause of %v (cause: %v) != %v", err, errors.Unwrap(err), testcase.cause)
				}
			} else if testcase.cause != nil {
				return
			}

			checkContainerTimestamps(t, &result, now, false)
			testcase.expected.UpdatedAt = result.UpdatedAt
			checkContainersEqual(t, &result, &testcase.expected, "updated failed to get expected result")

			result, err = store.Get(ctx, testcase.original.ID)
			if err != nil {
				t.Fatal(err)
			}

			checkContainersEqual(t, &result, &testcase.expected, "get after failed to get expected result")
		})
	}
}

func checkContainerTimestamps(t *testing.T, c *containers.Container, now time.Time, oncreate bool) {
	if c.UpdatedAt.IsZero() || c.CreatedAt.IsZero() {
		t.Fatalf("timestamps not set")
	}

	if oncreate {
		if !c.CreatedAt.Equal(c.UpdatedAt) {
			t.Fatal("timestamps should be equal on create")
		}

	} else {
		// ensure that updatedat is always after createdat
		if !c.UpdatedAt.After(c.CreatedAt) {
			if runtime.GOOS == "windows" && c.UpdatedAt == c.CreatedAt {
				// Windows' time.Now resolution is lower than Linux, due to Go.
				// https://github.com/golang/go/issues/31160
			} else {
				t.Fatalf("timestamp for updatedat not after createdat: %v <= %v", c.UpdatedAt, c.CreatedAt)
			}
		}
	}

	if c.UpdatedAt.Before(now) {
		t.Fatal("createdat time incorrect should be after the start of the operation")
	}
}

func checkContainersEqual(t *testing.T, a, b *containers.Container, format string, args ...interface{}) {
	assert.True(t, cmp.Equal(a, b, compareNil, compareAny))
}

func testEnv(t *testing.T) (context.Context, *bolt.DB) {
	ctx, cancel := context.WithCancel(context.Background())
	ctx = namespaces.WithNamespace(ctx, "testing")
	ctx = logtest.WithT(ctx, t)
	dirname := t.TempDir()

	db, err := bolt.Open(filepath.Join(dirname, "meta.db"), 0644, nil)
	require.NoError(t, err)

	t.Cleanup(func() {
		assert.NoError(t, db.Close())
		cancel()
	})

	return ctx, db
}
