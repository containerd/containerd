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
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/containerd/containerd/api/services/introspection/v1"
	"github.com/stretchr/testify/assert"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

type fakeIntrospectionService struct {
}

func (f fakeIntrospectionService) Plugins(context.Context, ...string) (*introspection.PluginsResponse, error) {
	return nil, errors.New("not implemented")
}

func (f fakeIntrospectionService) Server(ctx context.Context) (*introspection.ServerResponse, error) {
	return &introspection.ServerResponse{Deprecations: []*introspection.DeprecationWarning{}}, nil
}

func (f fakeIntrospectionService) PluginInfo(context.Context, string, string, any) (*introspection.PluginInfoResponse, error) {
	return nil, errors.New("not implemented")
}

// commonConfig is shared by all CRI container runtimes
type commonConfig struct {
	SandboxImage string `json:"sandboxImage,omitempty"`
}

func criConfig(sandboxImage string) (*commonConfig, error) {
	// need Client IntrospectionService for Server Deprecations, or Status will crash
	c := newTestCRIService(withClientIntrospectionService(&fakeIntrospectionService{}))
	if sandboxImage != "" {
		c.ImageService = &fakeImageService{pinnedImages: map[string]string{"sandbox": sandboxImage}}
	}
	resp, err := c.Status(context.Background(), &runtime.StatusRequest{Verbose: true})
	if err != nil {
		return nil, fmt.Errorf("failed to get status: %w", err)
	}
	config := &commonConfig{}
	if err := json.Unmarshal([]byte(resp.Info["config"]), config); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}
	return config, nil
}

func TestStatusConfig(t *testing.T) {
	// use default sandbox image from Client
	config, err := criConfig("")
	assert.NoError(t, err)
	assert.NotEqual(t, "", config.SandboxImage)
}

func TestStatusConfigSandboxImage(t *testing.T) {
	pause := "registry.k8s.io/pause:override"
	config, err := criConfig(pause)
	assert.NoError(t, err)
	assert.Equal(t, pause, config.SandboxImage)
}

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
