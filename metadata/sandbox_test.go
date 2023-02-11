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
	"github.com/containerd/containerd/protobuf/types"
	api "github.com/containerd/containerd/sandbox"
	"github.com/containerd/typeurl/v2"
	"github.com/google/go-cmp/cmp"
)

func TestSandboxCreate(t *testing.T) {
	ctx, db := testDB(t)

	store := NewSandboxStore(db)

	in := api.Sandbox{
		ID:     "1",
		Labels: map[string]string{"a": "1", "b": "2"},
		Spec:   &types.Any{TypeUrl: "1", Value: []byte{1, 2, 3}},
		Extensions: map[string]typeurl.Any{
			"ext1": &types.Any{TypeUrl: "url/1", Value: []byte{1, 2, 3}},
			"ext2": &types.Any{TypeUrl: "url/2", Value: []byte{3, 2, 1}},
		},
		Runtime: api.RuntimeOpts{
			Name:    "test",
			Options: &types.Any{TypeUrl: "url/3", Value: []byte{4, 5, 6}},
		},
	}

	_, err := store.Create(ctx, in)
	if err != nil {
		t.Fatal(err)
	}

	out, err := store.Get(ctx, "1")
	if err != nil {
		t.Fatal(err)
	}

	assertEqualInstances(t, in, out)
}

func TestSandboxCreateDup(t *testing.T) {
	ctx, db := testDB(t)

	store := NewSandboxStore(db)

	in := api.Sandbox{
		ID:      "1",
		Spec:    &types.Any{TypeUrl: "1", Value: []byte{1, 2, 3}},
		Runtime: api.RuntimeOpts{Name: "test"},
	}

	_, err := store.Create(ctx, in)
	if err != nil {
		t.Fatal(err)
	}

	_, err = store.Create(ctx, in)
	if !errdefs.IsAlreadyExists(err) {
		t.Fatalf("expected %+v, got %+v", errdefs.ErrAlreadyExists, err)
	}
}

func TestSandboxUpdate(t *testing.T) {
	ctx, db := testDB(t)
	store := NewSandboxStore(db)

	if _, err := store.Create(ctx, api.Sandbox{
		ID:     "2",
		Labels: map[string]string{"lbl1": "existing"},
		Spec:   &types.Any{TypeUrl: "1", Value: []byte{1}}, // will replace
		Extensions: map[string]typeurl.Any{
			"ext2": &types.Any{TypeUrl: "url2", Value: []byte{4, 5, 6}}, // will append `ext1`
		},
		Runtime: api.RuntimeOpts{Name: "test"}, // no change
	}); err != nil {
		t.Fatal(err)
	}

	expectedSpec := types.Any{TypeUrl: "2", Value: []byte{3, 2, 1}}

	out, err := store.Update(ctx, api.Sandbox{
		ID:     "2",
		Labels: map[string]string{"lbl1": "new"},
		Spec:   &expectedSpec,
		Extensions: map[string]typeurl.Any{
			"ext1": &types.Any{TypeUrl: "url1", Value: []byte{1, 2}},
		},
	}, "labels.lbl1", "extensions.ext1", "spec")
	if err != nil {
		t.Fatal(err)
	}

	expected := api.Sandbox{
		ID:   "2",
		Spec: &expectedSpec,
		Labels: map[string]string{
			"lbl1": "new",
		},
		Extensions: map[string]typeurl.Any{
			"ext1": &types.Any{TypeUrl: "url1", Value: []byte{1, 2}},
			"ext2": &types.Any{TypeUrl: "url2", Value: []byte{4, 5, 6}},
		},
		Runtime: api.RuntimeOpts{Name: "test"},
	}

	assertEqualInstances(t, out, expected)
}

func TestSandboxGetInvalid(t *testing.T) {
	ctx, db := testDB(t)
	store := NewSandboxStore(db)

	_, err := store.Get(ctx, "invalid_id")
	if err == nil {
		t.Fatalf("expected %+v error for invalid ID", errdefs.ErrNotFound)
	} else if !errdefs.IsNotFound(err) {
		t.Fatalf("unexpected error %T type", err)
	}
}

func TestSandboxList(t *testing.T) {
	ctx, db := testDB(t)
	store := NewSandboxStore(db)

	in := []api.Sandbox{
		{
			ID:         "1",
			Labels:     map[string]string{"test": "1"},
			Spec:       &types.Any{TypeUrl: "1", Value: []byte{1, 2, 3}},
			Extensions: map[string]typeurl.Any{"ext": &types.Any{}},
			Runtime:    api.RuntimeOpts{Name: "test"},
		},
		{
			ID:     "2",
			Labels: map[string]string{"test": "2"},
			Spec:   &types.Any{TypeUrl: "2", Value: []byte{3, 2, 1}},
			Extensions: map[string]typeurl.Any{"ext": &types.Any{
				TypeUrl: "test",
				Value:   []byte{9},
			}},
			Runtime: api.RuntimeOpts{Name: "test"},
		},
	}

	for _, inst := range in {
		_, err := store.Create(ctx, inst)
		if err != nil {
			t.Fatal(err)
		}
	}

	out, err := store.List(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if len(in) != len(out) {
		t.Fatalf("expected list size: %d != %d", len(in), len(out))
	}

	for i := range out {
		assertEqualInstances(t, out[i], in[i])
	}
}

func TestSandboxListWithFilter(t *testing.T) {
	ctx, db := testDB(t)
	store := NewSandboxStore(db)

	in := []api.Sandbox{
		{
			ID:         "1",
			Labels:     map[string]string{"test": "1"},
			Spec:       &types.Any{TypeUrl: "1", Value: []byte{1, 2, 3}},
			Extensions: map[string]typeurl.Any{"ext": &types.Any{}},
			Runtime:    api.RuntimeOpts{Name: "test"},
		},
		{
			ID:     "2",
			Labels: map[string]string{"test": "2"},
			Spec:   &types.Any{TypeUrl: "2", Value: []byte{3, 2, 1}},
			Extensions: map[string]typeurl.Any{"ext": &types.Any{
				TypeUrl: "test",
				Value:   []byte{9},
			}},
			Runtime: api.RuntimeOpts{Name: "test"},
		},
	}

	for _, inst := range in {
		_, err := store.Create(ctx, inst)
		if err != nil {
			t.Fatal(err)
		}
	}

	out, err := store.List(ctx, "id==1")
	if err != nil {
		t.Fatal(err)
	}

	if len(out) != 1 {
		t.Fatalf("expected list to contain 1 element, got %d", len(out))
	}

	assertEqualInstances(t, out[0], in[0])
}

func TestSandboxDelete(t *testing.T) {
	ctx, db := testDB(t)

	store := NewSandboxStore(db)

	in := api.Sandbox{
		ID:      "2",
		Spec:    &types.Any{TypeUrl: "1", Value: []byte{1, 2, 3}},
		Runtime: api.RuntimeOpts{Name: "test"},
	}

	_, err := store.Create(ctx, in)
	if err != nil {
		t.Fatal(err)
	}

	err = store.Delete(ctx, "2")
	if err != nil {
		t.Fatalf("deleted failed %+v", err)
	}

	_, err = store.Get(ctx, "2")
	if !errdefs.IsNotFound(err) {
		t.Fatalf("unexpected err result: %+v != %+v", err, errdefs.ErrNotFound)
	}
}

func assertEqualInstances(t *testing.T, x, y api.Sandbox) {
	diff := cmp.Diff(x, y, compareNil, compareAny, ignoreTime)
	if diff != "" {
		t.Fatalf("x and y are different: %s", diff)
	}
}
