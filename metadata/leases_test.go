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
	_ "crypto/sha256"
	"errors"
	"fmt"
	"testing"

	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/leases"

	bolt "go.etcd.io/bbolt"
)

func TestLeases(t *testing.T) {
	ctx, db, cancel := testEnv(t)
	defer cancel()

	lm := NewLeaseManager(NewDB(db, nil, nil))

	testCases := []struct {
		ID        string
		CreateErr error
		DeleteErr error
	}{
		{
			ID: "tx1",
		},
		{
			ID:        "tx1",
			CreateErr: errdefs.ErrAlreadyExists,
			DeleteErr: errdefs.ErrNotFound,
		},
		{
			ID: "tx2",
		},
	}

	var ll []leases.Lease

	for _, tc := range testCases {
		if err := db.Update(func(tx *bolt.Tx) error {
			lease, err := lm.Create(WithTransactionContext(ctx, tx), leases.WithID(tc.ID))
			if err != nil {
				if tc.CreateErr != nil && errors.Is(err, tc.CreateErr) {
					return nil
				}
				return err
			}
			ll = append(ll, lease)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}

	listed, err := lm.List(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if len(listed) != len(ll) {
		t.Fatalf("Expected %d lease, got %d", len(ll), len(listed))
	}
	for i := range listed {
		if listed[i].ID != ll[i].ID {
			t.Fatalf("Expected lease ID %s, got %s", ll[i].ID, listed[i].ID)
		}
		if listed[i].CreatedAt != ll[i].CreatedAt {
			t.Fatalf("Expected lease created at time %s, got %s", ll[i].CreatedAt, listed[i].CreatedAt)
		}
	}

	for _, tc := range testCases {
		if err := lm.Delete(ctx, leases.Lease{
			ID: tc.ID,
		}); err != nil {
			if tc.DeleteErr == nil && !errors.Is(err, tc.DeleteErr) {
				t.Fatal(err)
			}

		}
	}

	listed, err = lm.List(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if len(listed) > 0 {
		t.Fatalf("Expected no leases, found %d: %v", len(listed), listed)
	}
}

func TestLeasesList(t *testing.T) {
	ctx, db, cancel := testEnv(t)
	defer cancel()

	lm := NewLeaseManager(NewDB(db, nil, nil))

	testset := [][]leases.Opt{
		{
			leases.WithID("lease1"),
			leases.WithLabels(map[string]string{
				"label1": "value1",
				"label3": "other",
			}),
		},
		{
			leases.WithID("lease2"),
			leases.WithLabels(map[string]string{
				"label1": "value1",
				"label2": "",
				"label3": "other",
			}),
		},
		{
			leases.WithID("lease3"),
			leases.WithLabels(map[string]string{
				"label1": "value2",
				"label2": "something",
			}),
		},
	}

	// Insert all
	if err := db.Update(func(tx *bolt.Tx) error {
		for _, opts := range testset {
			_, err := lm.Create(WithTransactionContext(ctx, tx), opts...)
			if err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	for _, testcase := range []struct {
		name     string
		filters  []string
		expected []string
	}{
		{
			name:     "All",
			filters:  []string{},
			expected: []string{"lease1", "lease2", "lease3"},
		},
		{
			name:     "ID",
			filters:  []string{"id==lease1"},
			expected: []string{"lease1"},
		},
		{
			name:     "IDx2",
			filters:  []string{"id==lease1", "id==lease2"},
			expected: []string{"lease1", "lease2"},
		},
		{
			name:     "Label1",
			filters:  []string{"labels.label1"},
			expected: []string{"lease1", "lease2", "lease3"},
		},

		{
			name:     "Label1value1",
			filters:  []string{"labels.label1==value1"},
			expected: []string{"lease1", "lease2"},
		},
		{
			name:     "Label1value2",
			filters:  []string{"labels.label1==value2"},
			expected: []string{"lease3"},
		},
		{
			name:     "Label2",
			filters:  []string{"labels.label2"},
			expected: []string{"lease3"},
		},
		{
			name:     "Label3",
			filters:  []string{"labels.label2", "labels.label3"},
			expected: []string{"lease1", "lease2", "lease3"},
		},
	} {
		t.Run(testcase.name, func(t *testing.T) {
			results, err := lm.List(ctx, testcase.filters...)
			if err != nil {
				t.Fatal(err)
			}

			if len(results) != len(testcase.expected) {
				t.Errorf("length of result does not match expected: %v != %v", len(results), len(testcase.expected))
			}

			expectedMap := map[string]struct{}{}
			for _, expected := range testcase.expected {
				expectedMap[expected] = struct{}{}
			}

			for _, result := range results {
				if _, ok := expectedMap[result.ID]; !ok {
					t.Errorf("unexpected match: %v", result.ID)
				} else {
					delete(expectedMap, result.ID)
				}
			}
			if len(expectedMap) > 0 {
				for match := range expectedMap {
					t.Errorf("missing match: %v", match)
				}
			}

		})
	}

	// delete everything to test it
	for _, opts := range testset {
		var lease leases.Lease
		for _, opt := range opts {
			if err := opt(&lease); err != nil {
				t.Fatal(err)
			}
		}

		if err := lm.Delete(ctx, lease); err != nil {
			t.Fatal(err)
		}

		// try it again, get not found
		if err := lm.Delete(ctx, lease); err == nil {
			t.Fatalf("expected error deleting non-existent lease")
		} else if !errdefs.IsNotFound(err) {
			t.Fatalf("unexpected error: %s", err)
		}
	}
}

func TestLeaseResource(t *testing.T) {
	ctx, db, cancel := testEnv(t)
	defer cancel()

	lm := NewLeaseManager(NewDB(db, nil, nil))

	var (
		leaseID = "l1"

		lease = leases.Lease{
			ID: leaseID,
		}

		snapshotterKey = "RstMI3X8vguKoPFkmIStZ5fQFI7F1L0o"
	)

	// prepare lease
	if _, err := lm.Create(ctx, leases.WithID(leaseID)); err != nil {
		t.Fatal(err)
	}

	testCases := []struct {
		lease    leases.Lease
		resource leases.Resource
		err      error
	}{
		{
			lease: lease,
			resource: leases.Resource{
				ID:   "sha256:29f5d56d12684887bdfa50dcd29fc31eea4aaf4ad3bec43daf19026a7ce69912",
				Type: "content",
			},
		},
		{
			lease: lease,
			resource: leases.Resource{
				ID:   "d2UdcINOwrBTQG9kS8rySAM3eMNBSojH",
				Type: "ingests",
			},
		},
		{
			// allow to add resource which exists
			lease: lease,
			resource: leases.Resource{
				ID:   "d2UdcINOwrBTQG9kS8rySAM3eMNBSojH",
				Type: "ingests",
			},
		},
		{
			// not allow to reference to lease
			lease: lease,
			resource: leases.Resource{
				ID:   "xCAV3F6PddlXitbtby0Vo23Qof6RTWpG",
				Type: "leases",
			},
			err: errdefs.ErrNotImplemented,
		},
		{
			// not allow to reference to container
			lease: lease,
			resource: leases.Resource{
				ID:   "05O9ljptPu5Qq9kZGOacEfymBwQFM8ZH",
				Type: "containers",
			},
			err: errdefs.ErrNotImplemented,
		},
		{
			// not allow to reference to image
			lease: lease,
			resource: leases.Resource{
				ID:   "qBUHpWBn03YaCt9cL3PPGKWoxBqTlLfu",
				Type: "image",
			},
			err: errdefs.ErrNotImplemented,
		},
		{
			lease: lease,
			resource: leases.Resource{
				ID:   "HMemOhlygombYhkhHhAZj5aRbDy2a3z2",
				Type: "snapshots",
			},
			err: errdefs.ErrInvalidArgument,
		},
		{
			lease: lease,
			resource: leases.Resource{
				ID:   snapshotterKey,
				Type: "snapshots/overlayfs",
			},
		},
		{
			lease: lease,
			resource: leases.Resource{
				ID:   "HMemOhlygombYhkhHhAZj5aRbDy2a3z2",
				Type: "snapshots/overlayfs/type1",
			},
			err: errdefs.ErrInvalidArgument,
		},
		{
			lease: leases.Lease{
				ID: "non-found",
			},
			resource: leases.Resource{
				ID:   "HMemOhlygombYhkhHhAZj5aRbDy2a3z2",
				Type: "snapshots/overlayfs",
			},
			err: errdefs.ErrNotFound,
		},
	}

	idxList := make(map[leases.Resource]bool)
	for i, tc := range testCases {
		if err := db.Update(func(tx *bolt.Tx) error {
			err0 := lm.AddResource(WithTransactionContext(ctx, tx), tc.lease, tc.resource)
			if !errors.Is(err0, tc.err) {
				return fmt.Errorf("expect error (%v), but got (%v)", tc.err, err0)
			}

			if err0 == nil {
				// not visited yet
				idxList[tc.resource] = false
			}
			return nil
		}); err != nil {
			t.Fatalf("failed to run case %d with resource: %v", i, err)
		}
	}

	// check list function
	var gotList []leases.Resource
	gotList, err := lm.ListResources(ctx, lease)
	if err != nil {
		t.Fatal(err)
	}

	if len(gotList) != len(idxList) {
		t.Fatalf("expected (%d) resources, but got (%d)", len(idxList), len(gotList))
	}

	for _, r := range gotList {
		visited, ok := idxList[r]
		if !ok {
			t.Fatalf("unexpected resource(%v)", r)
		}
		if visited {
			t.Fatalf("duplicate resource(%v)", r)
		}
		idxList[r] = true
	}

	// remove snapshots
	if err := lm.DeleteResource(ctx, lease, leases.Resource{
		ID:   snapshotterKey,
		Type: "snapshots/overlayfs",
	}); err != nil {
		t.Fatal(err)
	}

	// check list number
	gotList, err = lm.ListResources(ctx, lease)
	if err != nil {
		t.Fatal(err)
	}

	if len(gotList)+1 != len(idxList) {
		t.Fatalf("expected (%d) resources, but got (%d)", len(idxList)-1, len(gotList))
	}
}
