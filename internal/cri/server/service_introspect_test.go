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
	"fmt"
	"testing"

	introspectionapi "github.com/containerd/containerd/api/services/introspection/v1"
	apitypes "github.com/containerd/containerd/api/types"
	"github.com/containerd/typeurl/v2"
	"github.com/opencontainers/runtime-spec/specs-go/features"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	coreintrospection "github.com/containerd/containerd/v2/core/introspection"
	criconfig "github.com/containerd/containerd/v2/internal/cri/config"
)

// fakeRuntimeIntrospection records the request it received and replies with a
// canned PluginInfo response, standing in for a shim's "-info" output.
type fakeRuntimeIntrospection struct {
	resp   *introspectionapi.PluginInfoResponse
	err    error
	gotReq *apitypes.RuntimeRequest
}

var _ coreintrospection.Service = (*fakeRuntimeIntrospection)(nil)

func (f *fakeRuntimeIntrospection) Plugins(context.Context, ...string) (*introspectionapi.PluginsResponse, error) {
	return &introspectionapi.PluginsResponse{}, nil
}

func (f *fakeRuntimeIntrospection) Server(context.Context) (*introspectionapi.ServerResponse, error) {
	return &introspectionapi.ServerResponse{}, nil
}

func (f *fakeRuntimeIntrospection) PluginInfo(_ context.Context, _ string, _ string, req any) (*introspectionapi.PluginInfoResponse, error) {
	rr, ok := req.(*apitypes.RuntimeRequest)
	if !ok {
		return nil, fmt.Errorf("unexpected request type %T", req)
	}
	f.gotReq = rr
	return f.resp, f.err
}

// runtimeInfoResponse wraps features into the nested Any-in-Any shape that
// introspectRuntimeFeatures expects from the introspection service.
func runtimeInfoResponse(t *testing.T, feat *features.Features) *introspectionapi.PluginInfoResponse {
	t.Helper()
	featAny, err := typeurl.MarshalAny(feat)
	require.NoError(t, err)
	infoAny, err := typeurl.MarshalAny(&apitypes.RuntimeInfo{Features: typeurl.MarshalProto(featAny)})
	require.NoError(t, err)
	return &introspectionapi.PluginInfoResponse{Extra: typeurl.MarshalProto(infoAny)}
}

func boolPtr(b bool) *bool { return &b }

// TestIntrospectRuntimeFeaturesNonRunc verifies that runtimes other than
// io.containerd.runc.v2 are now introspected instead of being rejected, which
// is what lets shims such as containerd-shim-runsc-v1 surface OCI features.
func TestIntrospectRuntimeFeaturesNonRunc(t *testing.T) {
	feat := &features.Features{MountOptions: []string{"rro"}}

	for _, tc := range []struct {
		name        string
		runtime     criconfig.Runtime
		wantOptions bool
	}{
		{
			name:    "runsc without options",
			runtime: criconfig.Runtime{Type: "io.containerd.runsc.v1"},
		},
		{
			// Exercises the options marshalling that the previous runc-only
			// guard avoided; generic runtimes use runtimeoptions.Options.
			name:        "runsc with options",
			runtime:     criconfig.Runtime{Type: "io.containerd.runsc.v1", Options: map[string]any{"ConfigPath": "/etc/containerd/runsc.toml"}},
			wantOptions: true,
		},
		{
			name:    "runc still introspected",
			runtime: criconfig.Runtime{Type: "io.containerd.runc.v2"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			intro := &fakeRuntimeIntrospection{resp: runtimeInfoResponse(t, feat)}

			got, err := introspectRuntimeFeatures(context.Background(), intro, tc.runtime)
			require.NoError(t, err)
			assert.Equal(t, feat.MountOptions, got.MountOptions)

			require.NotNil(t, intro.gotReq)
			assert.Equal(t, tc.runtime.Type, intro.gotReq.RuntimePath)
			assert.Equal(t, tc.wantOptions, intro.gotReq.Options != nil)
		})
	}
}

// TestIntrospectRuntimeHandlerUserns checks that a non-runc handler advertises
// CRI user namespace support only when the shim reports both the user namespace
// and idmap mounts, matching kubelet's RuntimeHandlerFeatures expectations.
func TestIntrospectRuntimeHandlerUserns(t *testing.T) {
	for _, tc := range []struct {
		name string
		feat *features.Features
		want bool
	}{
		{
			name: "userns and idmap",
			feat: &features.Features{Linux: &features.Linux{
				Namespaces:      []string{"user"},
				MountExtensions: &features.MountExtensions{IDMap: &features.IDMap{Enabled: boolPtr(true)}},
			}},
			want: true,
		},
		{
			name: "userns without idmap",
			feat: &features.Features{Linux: &features.Linux{Namespaces: []string{"user"}}},
			want: false,
		},
		{
			name: "features without linux block",
			feat: &features.Features{MountOptions: []string{"rro"}},
			want: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &criService{runtimeHandlers: make(map[string]*runtime.RuntimeHandler)}
			intro := &fakeRuntimeIntrospection{resp: runtimeInfoResponse(t, tc.feat)}

			err := c.introspectRuntimeHandler(context.Background(), intro, "runsc", criconfig.Runtime{Type: "io.containerd.runsc.v1"})
			require.NoError(t, err)

			h := c.runtimeHandlers["runsc"]
			require.NotNil(t, h)
			require.NotNil(t, h.Features)
			assert.Equal(t, tc.want, h.Features.UserNamespaces)
		})
	}
}
