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
	"testing"

	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/oci"
	"github.com/containerd/containerd/sandbox"
	"github.com/containerd/typeurl"
	"github.com/google/go-cmp/cmp"
	"github.com/opencontainers/runtime-spec/specs-go"
	"google.golang.org/protobuf/types/known/anypb"
)

func withSandbox(name string, controller sandbox.Controller) testOpt {
	return func(to *testOptions) {
		if to.extraSandboxes == nil {
			to.extraSandboxes = map[string]sandbox.Controller{}
		}
		to.extraSandboxes[name] = controller
	}
}

func TestSandboxStart(t *testing.T) {
	ctx, db := testDB(t, withSandbox("test", &mockController{}))

	sb := db.Sandboxes()
	store := sb["test"]

	in := sandbox.Sandbox{
		ID:         "1",
		Labels:     map[string]string{"test": "1"},
		Spec:       &oci.Spec{},
		Extensions: map[string]typeurl.Any{},
	}

	out, err := store.Create(ctx, &in)

	if err != nil {
		t.Fatal(err)
	}

	if out.ID != "1" {
		t.Fatalf("unexpected instance ID: %q", out.ID)
	}

	if out.CreatedAt.IsZero() {
		t.Fatal("creation time not assigned")
	}

	if out.UpdatedAt.IsZero() {
		t.Fatal("updated time not assigned")
	}

	// Read back
	out, err = store.Get(ctx, "1")
	if err != nil {
		t.Fatal(err)
	}

	assertEqualInstances(t, &in, out)
}

func TestSandboxRollbackStart(t *testing.T) {
	ctx, db := testDB(t, withSandbox("test", &mockController{err: errors.New("failed to start")}))

	sb := db.Sandboxes()
	store := sb["test"]

	_, err := store.Create(ctx, &sandbox.Sandbox{
		ID:     "1",
		Labels: map[string]string{"test": "1"},
		Spec:   &oci.Spec{},
	})

	if err == nil {
		t.Fatal("expected start error")
	}

	_, err = store.Get(ctx, "1")
	if err == nil {
		t.Fatal("should not have saved failed instance to store")
	} else if !errdefs.IsNotFound(err) {
		t.Fatal("expected 'not found' error")
	}
}

func TestSandboxStartDup(t *testing.T) {
	ctx, db := testDB(t, withSandbox("test", &mockController{}))

	sb := db.Sandboxes()
	store := sb["test"]

	in := &sandbox.Sandbox{
		ID:     "1",
		Labels: map[string]string{"test": "1"},
		Spec:   &oci.Spec{},
	}

	_, err := store.Create(ctx, in)
	if err != nil {
		t.Fatal(err)
	}

	_, err = store.Create(ctx, in)
	if err == nil {
		t.Fatal("should return error on double start")
	} else if !errdefs.IsAlreadyExists(err) {
		t.Fatalf("unexpected error: %+v", err)
	}
}

func TestSandboxDelete(t *testing.T) {
	ctx, db := testDB(t, withSandbox("test", &mockController{
		update: func(action string, instance *sandbox.Sandbox) (*sandbox.Sandbox, error) {
			if action == "stop" {
				instance.Labels["test"] = "updated"
			}
			return instance, nil
		},
	}))

	sb := db.Sandboxes()
	store := sb["test"]

	_, err := store.Create(ctx, &sandbox.Sandbox{
		ID:     "1",
		Labels: map[string]string{"test": "1"},
		Spec:   &oci.Spec{},
	})

	if err != nil {
		t.Fatal(err)
	}

	err = store.Delete(ctx, "1")
	if err != nil {
		t.Fatal(err)
	}

	// Read back, make sure dabase got updated instance
	_, err = store.Get(ctx, "1")
	if err == nil {
		t.Fatal("expected 'not found' error")
	}
}

func TestSandboxStopInvalid(t *testing.T) {
	ctx, db := testDB(t, withSandbox("test", &mockController{}))

	sb := db.Sandboxes()
	store := sb["test"]

	err := store.Delete(ctx, "invalid_id")
	if err == nil {
		t.Fatal("expected 'not found' error for invalid ID")
	} else if !errdefs.IsNotFound(err) {
		t.Fatalf("unexpected error %T type", err)
	}
}

func TestSandboxUpdate(t *testing.T) {
	ctx, db := testDB(t, withSandbox("test", &mockController{
		update: func(action string, instance *sandbox.Sandbox) (*sandbox.Sandbox, error) {
			if action == "update" {
				instance.Labels["lbl2"] = "updated"
				instance.Extensions["ext2"] = &anypb.Any{
					TypeUrl: "url2",
					Value:   []byte{4, 5, 6},
				}
			}
			return instance, nil
		},
	}))

	sb := db.Sandboxes()
	store := sb["test"]

	_, err := store.Create(ctx, &sandbox.Sandbox{
		ID:   "2",
		Spec: &oci.Spec{},
	})

	if err != nil {
		t.Fatal(err)
	}

	expectedSpec := &oci.Spec{
		Version:  "1.0.0",
		Hostname: "localhost",
		Linux: &specs.Linux{
			Sysctl: map[string]string{"a": "b"},
		},
	}

	out, err := store.Update(ctx, &sandbox.Sandbox{
		ID:     "2",
		Labels: map[string]string{"lbl1": "new"},
		Spec:   expectedSpec,
		Extensions: map[string]typeurl.Any{
			"ext1": &anypb.Any{TypeUrl: "url1", Value: []byte{1, 2}},
		},
	}, "labels.lbl1", "extensions.ext1", "spec")

	if err != nil {
		t.Fatal(err)
	}

	expected := &sandbox.Sandbox{
		ID:   "2",
		Spec: expectedSpec,
		Labels: map[string]string{
			"lbl1": "new",
			"lbl2": "updated",
		},
		Extensions: map[string]typeurl.Any{
			"ext1": &anypb.Any{TypeUrl: "url1", Value: []byte{1, 2}},
			"ext2": &anypb.Any{TypeUrl: "url2", Value: []byte{4, 5, 6}},
		},
	}

	assertEqualInstances(t, out, expected)
}

func TestSandboxFindInvalid(t *testing.T) {
	ctx, db := testDB(t, withSandbox("test", &mockController{}))

	sb := db.Sandboxes()
	store := sb["test"]

	_, err := store.Get(ctx, "invalid_id")
	if err == nil {
		t.Fatal("expected 'not found' error for invalid ID")
	} else if !errdefs.IsNotFound(err) {
		t.Fatalf("unexpected error %T type", err)
	}
}

func TestSandboxList(t *testing.T) {
	ctx, db := testDB(t, withSandbox("test", &mockController{}))

	sb := db.Sandboxes()
	store := sb["test"]

	in := []*sandbox.Sandbox{
		{
			ID:         "1",
			Labels:     map[string]string{"test": "1"},
			Spec:       &oci.Spec{},
			Extensions: map[string]typeurl.Any{"ext": &anypb.Any{}},
		},
		{
			ID:     "2",
			Labels: map[string]string{"test": "2"},
			Spec:   &oci.Spec{},
			Extensions: map[string]typeurl.Any{"ext": &anypb.Any{
				TypeUrl: "test",
				Value:   []byte{9},
			}},
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
		assertEqualInstances(t, &out[i], in[i])
	}
}

func TestSandboxListFilter(t *testing.T) {
	ctx, db := testDB(t, withSandbox("test", &mockController{}))

	sb := db.Sandboxes()
	store := sb["test"]

	in := []*sandbox.Sandbox{
		{
			ID:         "1",
			Labels:     map[string]string{"test": "1"},
			Spec:       &oci.Spec{},
			Extensions: map[string]typeurl.Any{"ext": &anypb.Any{}},
		},
		{
			ID:     "2",
			Labels: map[string]string{"test": "2"},
			Spec:   &oci.Spec{},
			Extensions: map[string]typeurl.Any{"ext": &anypb.Any{
				TypeUrl: "test",
				Value:   []byte{9},
			}},
		},
	}

	for _, inst := range in {
		_, err := store.Create(ctx, inst)
		if err != nil {
			t.Fatal(err)
		}
	}

	out, err := store.List(ctx, `id==1`)
	if err != nil {
		t.Fatal(err)
	}

	if len(out) != 1 {
		t.Fatalf("expected list to contain 1 element, got %d", len(out))
	}

	assertEqualInstances(t, &out[0], in[0])
}

func TestSandboxReadWrite(t *testing.T) {
	ctx, db := testDB(t, withSandbox("test", &mockController{}))

	sb := db.Sandboxes()
	store := sb["test"]

	in := &sandbox.Sandbox{
		ID:     "1",
		Labels: map[string]string{"a": "1", "b": "2"},
		Spec: &oci.Spec{
			Version:  "1.0.0",
			Hostname: "localhost",
			Linux: &specs.Linux{
				Sysctl: map[string]string{"a": "b"},
			},
		},
		Extensions: map[string]typeurl.Any{
			"ext1": &anypb.Any{TypeUrl: "url/1", Value: []byte{1, 2, 3}},
			"ext2": &anypb.Any{TypeUrl: "url/2", Value: []byte{3, 2, 1}},
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

func assertEqualInstances(t *testing.T, x, y *sandbox.Sandbox) {
	diff := cmp.Diff(x, y, compareNil, compareAny, ignoreTime)
	if diff != "" {
		t.Fatalf("x and y are different: %s", diff)
	}
}

// A passthru sandbox controller for testing
type mockController struct {
	err    error
	update func(action string, instance *sandbox.Sandbox) (*sandbox.Sandbox, error)
}

func (m *mockController) Start(ctx context.Context, sandbox *sandbox.Sandbox) (*sandbox.Sandbox, error) {
	if m.update != nil {
		return m.update("start", sandbox)
	}
	return sandbox, m.err
}

func (m *mockController) Shutdown(ctx context.Context, id string) error {
	return m.err
}

func (m *mockController) Pause(ctx context.Context, id string) error {
	return m.err
}

func (m *mockController) Resume(ctx context.Context, id string) error {
	return m.err
}

func (m *mockController) Update(ctx context.Context, sandboxID string, sandbox *sandbox.Sandbox) (*sandbox.Sandbox, error) {
	if m.update != nil {
		return m.update("update", sandbox)
	}
	return sandbox, m.err
}

func (m *mockController) AppendContainer(ctx context.Context, sandboxID string, container *sandbox.Container) (*sandbox.Container, error) {
	return nil, m.err
}

func (m *mockController) UpdateContainer(ctx context.Context, sandboxID string, container *sandbox.Container) (*sandbox.Container, error) {
	return nil, m.err
}

func (m *mockController) RemoveContainer(ctx context.Context, sandboxID string, id string) error {
	return m.err
}

func (m *mockController) Status(ctx context.Context, id string) (sandbox.Status, error) {
	return sandbox.Status{}, m.err
}

func (m *mockController) Ping(ctx context.Context, id string) error {
	return m.err
}

var _ sandbox.Controller = &mockController{}
