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

package server

import (
	"context"
	"reflect"
	"testing"
	"unsafe"

	"github.com/containerd/containerd/api/services/introspection/v1"
	containerd "github.com/containerd/containerd/v2/client"
	coreintrospection "github.com/containerd/containerd/v2/core/introspection"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

func TestRuntimeConditionContainerdHasNoDeprecationWarnings(t *testing.T) {
	deprecations := []*introspection.DeprecationWarning{
		{
			ID:      "io.containerd.deprecation/foo",
			Message: "foo",
		},
	}

	cond, err := runtimeConditionContainerdHasNoDeprecationWarnings(deprecations, nil)
	assert.NoError(t, err)
	assert.Equal(t, &runtime.RuntimeCondition{
		Type:    ContainerdHasNoDeprecationWarnings,
		Status:  false,
		Reason:  ContainerdHasDeprecationWarnings,
		Message: `{"io.containerd.deprecation/foo":"foo"}`,
	}, cond)

	cond, err = runtimeConditionContainerdHasNoDeprecationWarnings(deprecations, []string{"io.containerd.deprecation/foo"})
	assert.NoError(t, err)
	assert.Equal(t, &runtime.RuntimeCondition{
		Type:   ContainerdHasNoDeprecationWarnings,
		Status: true,
	}, cond)
}

// fakeIntrospectionService is a minimal stub that implements the
// coreintrospection.Service. We need this because criService.Status()
// invokes the client.IntrospectionService() method.
type fakeIntrospectionService struct{}

var _ coreintrospection.Service = fakeIntrospectionService{}

func (fakeIntrospectionService) Plugins(ctx context.Context, _ ...string) (*introspection.PluginsResponse, error) {
	return &introspection.PluginsResponse{}, nil
}

func (fakeIntrospectionService) Server(ctx context.Context) (*introspection.ServerResponse, error) {
	return &introspection.ServerResponse{}, nil
}

func (fakeIntrospectionService) PluginInfo(ctx context.Context, _ string, _ string, _ any) (*introspection.PluginInfoResponse, error) {
	return &introspection.PluginInfoResponse{}, nil
}

// newFakeContainerdClient returns a *containerd.Client with a stub
// IntrospectionService injected via reflection. This avoids needing a real
// gRPC connection while satisfying criService.Status().
func newFakeContainerdClient() *containerd.Client {
	c := &containerd.Client{}
	sv := reflect.ValueOf(c).Elem().FieldByName("services")
	if !sv.IsValid() {
		return c
	}
	f := sv.FieldByName("introspectionService")
	if !f.IsValid() {
		return c
	}
	// Make the unexported/private field introspectionService settable
	f = reflect.NewAt(f.Type(), unsafe.Pointer(f.UnsafeAddr())).Elem()
	f.Set(reflect.ValueOf(fakeIntrospectionService{}).Convert(f.Type()))
	return c
}

// newStatusTestCRIService creates a minimal CRI service for testing
func newStatusTestCRIService() *criService {
	return &criService{
		client:          newFakeContainerdClient(),
		runtimeHandlers: make(map[string]*runtime.RuntimeHandler),
	}
}

// TestStatusRuntimeHandlersOrdering checks that the runtime handlers
// returned by Status() are in the same order every time
func TestStatusRuntimeHandlersOrdering(t *testing.T) {
	c := newStatusTestCRIService()

	// Forge many runtime handlers to lower risk of accidental stable
	// ordering on consecutive Status() calls
	const numHandlers = 100
	handlers := make(map[string]*runtime.RuntimeHandler, numHandlers)
	for range numHandlers {
		h := &runtime.RuntimeHandler{Name: "random-" + uuid.New().String()}
		handlers[h.Name] = h
	}
	c.runtimeHandlers = handlers

	// Call Status() twice
	resp1, err := c.Status(context.Background(), &runtime.StatusRequest{})
	assert.NoError(t, err)
	assert.Len(t, resp1.RuntimeHandlers, len(handlers), "Unexpected number of runtime handlers")

	resp2, err := c.Status(context.Background(), &runtime.StatusRequest{})
	assert.NoError(t, err)
	assert.Len(t, resp2.RuntimeHandlers, len(handlers), "Unexpected number of runtime handlers")

	// Check runtime handlers are in the same order
	sameOrder := true
	for i := 0; i < len(resp1.RuntimeHandlers); i++ {
		if resp1.RuntimeHandlers[i].Name != resp2.RuntimeHandlers[i].Name {
			sameOrder = false
			break
		}
	}

	// Fail if runtime handlers order varies across calls to Status()
	assert.True(t, sameOrder, "RuntimeHandlers order is unstable across Status() calls")
}
