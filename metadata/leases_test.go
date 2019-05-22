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
	"testing"

	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/leases"
	"github.com/pkg/errors"
	bolt "go.etcd.io/bbolt"
)

func TestLeases(t *testing.T) {
	ctx, db, cancel := testEnv(t)
	defer cancel()

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
			lease, err := NewLeaseManager(tx).Create(ctx, leases.WithID(tc.ID))
			if err != nil {
				if tc.CreateErr != nil && errors.Cause(err) == tc.CreateErr {
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

	var listed []leases.Lease
	// List leases, check same
	if err := db.View(func(tx *bolt.Tx) error {
		var err error
		listed, err = NewLeaseManager(tx).List(ctx)
		return err
	}); err != nil {
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
		if err := db.Update(func(tx *bolt.Tx) error {
			return NewLeaseManager(tx).Delete(ctx, leases.Lease{
				ID: tc.ID,
			})
		}); err != nil {
			if tc.DeleteErr == nil && errors.Cause(err) != tc.DeleteErr {
				t.Fatal(err)
			}

		}
	}

	if err := db.View(func(tx *bolt.Tx) error {
		var err error
		listed, err = NewLeaseManager(tx).List(ctx)
		return err
	}); err != nil {
		t.Fatal(err)
	}

	if len(listed) > 0 {
		t.Fatalf("Expected no leases, found %d: %v", len(listed), listed)
	}
}

func TestLeasesList(t *testing.T) {
	ctx, db, cancel := testEnv(t)
	defer cancel()

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
	for _, opts := range testset {
		if err := db.Update(func(tx *bolt.Tx) error {
			lm := NewLeaseManager(tx)
			_, err := lm.Create(ctx, opts...)
			return err
		}); err != nil {
			t.Fatal(err)
		}
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
			if err := db.View(func(tx *bolt.Tx) error {
				lm := NewLeaseManager(tx)
				results, err := lm.List(ctx, testcase.filters...)
				if err != nil {
					return err
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

				return nil
			}); err != nil {
				t.Fatal(err)
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

		if err := db.Update(func(tx *bolt.Tx) error {
			lm := NewLeaseManager(tx)
			return lm.Delete(ctx, lease)
		}); err != nil {
			t.Fatal(err)
		}

		// try it again, get not found
		if err := db.Update(func(tx *bolt.Tx) error {
			lm := NewLeaseManager(tx)
			return lm.Delete(ctx, lease)
		}); err == nil {
			t.Fatalf("expected error deleting non-existent lease")
		} else if !errdefs.IsNotFound(err) {
			t.Fatalf("unexpected error: %s", err)
		}
	}
}
