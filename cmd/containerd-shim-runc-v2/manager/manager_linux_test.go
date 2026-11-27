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

package manager

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/containerd/containerd/v2/internal/shimenv"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/containerd/containerd/v2/version"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/resource"
)

func TestNewCommandTracingResource(t *testing.T) {
	for _, tc := range []struct {
		name       string
		inherited  string
		attributes map[string]string
	}{
		{name: "empty"},
		{name: "whitespace", inherited: "  "},
		{
			name:      "inherited",
			inherited: "service.name=containerd,service.instance.id=daemon,service.version=old,service.namespace=runtime,host.name=node,custom.value=a%2Cb%3Dc%25",
			attributes: map[string]string{
				"service.namespace": "runtime",
				"host.name":         "node",
				"custom.value":      "a,b=c%",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("OTEL_SERVICE_NAME", "containerd")
			t.Setenv("OTEL_RESOURCE_ATTRIBUTES", tc.inherited)
			ctx := namespaces.WithNamespace(context.Background(), "test-ns")
			for _, id := range []string{"test-shim", "another-shim"} {
				cmd, err := newCommand(ctx, id, "/containerd.sock", "/containerd.sock.ttrpc", false)
				require.NoError(t, err)
				// Environ applies exec.Cmd's duplicate-variable handling. Read the
				// resulting resource with the SDK without using its cached default.
				for _, entry := range cmd.Environ() {
					key, value, _ := strings.Cut(entry, "=")
					if key == "OTEL_SERVICE_NAME" || key == "OTEL_RESOURCE_ATTRIBUTES" {
						t.Setenv(key, value)
					}
				}
				res, err := resource.New(ctx, resource.WithFromEnv())
				require.NoError(t, err)
				attrs := res.Set()
				for key, expected := range map[string]string{
					"service.name":        "containerd-shim-runc-v2",
					"service.instance.id": id,
					"service.version":     version.Version,
				} {
					value, ok := attrs.Value(attribute.Key(key))
					require.True(t, ok, key)
					require.Equal(t, expected, value.AsString(), key)
				}
				for key, expected := range tc.attributes {
					value, ok := attrs.Value(attribute.Key(key))
					require.True(t, ok, key)
					require.Equal(t, expected, value.AsString(), key)
				}
			}
		})
	}
}

func TestNewCommandGOMAXPROCS(t *testing.T) {
	for _, tc := range []struct {
		name      string
		inherited string // GOMAXPROCS in the shim's environment, if set
		unset     bool
		fromShim  []string // [plugins."io.containerd.shim.v1.manager"] env via CommandEnv, if set
		expected  string
	}{
		{name: "unset", unset: true, expected: "4"},
		{name: "empty", expected: "4"},
		{name: "default 2 from containerd helper becomes 4", inherited: "2", expected: "4"},
		{name: "explicit 1", inherited: "1", expected: "1"},
		{name: "explicit 2 from shim manager env preserved", fromShim: []string{"GOMAXPROCS=2"}, expected: "2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("GOMAXPROCS", tc.inherited)
			if tc.unset {
				require.NoError(t, os.Unsetenv("GOMAXPROCS"))
			}
			if len(tc.fromShim) > 0 {
				for _, entry := range shimenv.CommandEnv(os.Environ(), nil, tc.fromShim, true) {
					if key, val, ok := strings.Cut(entry, "="); ok && strings.Contains(key, "GOMAXPROCS") {
						t.Setenv(key, val)
					}
				}
			}
			ctx := namespaces.WithNamespace(context.Background(), "test-ns")
			cmd, err := newCommand(ctx, "test-shim", "/containerd.sock", "/containerd.sock.ttrpc", false)
			require.NoError(t, err)
			// Environ applies exec.Cmd's duplicate-variable handling.
			var values []string
			for _, entry := range cmd.Environ() {
				if value, ok := strings.CutPrefix(entry, "GOMAXPROCS="); ok {
					values = append(values, value)
				}
			}
			require.Equal(t, []string{tc.expected}, values)
		})
	}
}
