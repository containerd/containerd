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

package v2

import (
	"context"
	"testing"

	apitypes "github.com/containerd/containerd/api/types"
	"github.com/containerd/errdefs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDeprecatedMountCapabilitiesLookup(t *testing.T) {
	t.Run("nil receiver returns nil", func(t *testing.T) {
		var d *deprecatedMountCapabilities
		assert.Nil(t, d.lookup(context.Background(), testRuntimeName))
	})

	t.Run("well known default runtimes are never queried", func(t *testing.T) {
		for _, name := range []string{"io.containerd.runc.v2", "io.containerd.runhcs.v1"} {
			queried := false
			d := &deprecatedMountCapabilities{
				queryRuntimeInfo: func(context.Context, string) (*apitypes.RuntimeInfo, error) {
					queried = true
					return &apitypes.RuntimeInfo{}, nil
				},
			}
			assert.Nil(t, d.lookup(context.Background(), name))
			assert.False(t, queried, "must not query %q", name)
		}
	})

	t.Run("annotation present is parsed", func(t *testing.T) {
		d := &deprecatedMountCapabilities{
			queryRuntimeInfo: func(context.Context, string) (*apitypes.RuntimeInfo, error) {
				return &apitypes.RuntimeInfo{
					Annotations: map[string]string{
						deprecatedAllowedMounts: "erofs,format/*",
					},
				}, nil
			},
		}

		caps := d.lookup(context.Background(), testRuntimeName)
		require.NotNil(t, caps)
		assert.Equal(t, []string{"erofs"}, caps.Types)
		assert.Equal(t, []string{"format"}, caps.Transforms)
	})

	t.Run("annotation absent returns nil", func(t *testing.T) {
		d := &deprecatedMountCapabilities{
			queryRuntimeInfo: func(context.Context, string) (*apitypes.RuntimeInfo, error) {
				return &apitypes.RuntimeInfo{}, nil
			},
		}
		assert.Nil(t, d.lookup(context.Background(), testRuntimeName))
	})

	t.Run("a query error returns nil rather than failing", func(t *testing.T) {
		d := &deprecatedMountCapabilities{
			queryRuntimeInfo: func(context.Context, string) (*apitypes.RuntimeInfo, error) {
				return nil, errdefs.ErrUnavailable
			},
		}
		assert.Nil(t, d.lookup(context.Background(), testRuntimeName))
	})

	// A transient failure (a momentary fork failure, for example) must not be
	// cached as a permanent "nothing to migrate" result: that would silently
	// and permanently disable the migration path for that runtime for the
	// life of the daemon, unlike the loadShimInfo mechanism this replaced,
	// which retried on the next Create.
	t.Run("a query error is not cached and is retried", func(t *testing.T) {
		calls := 0
		d := &deprecatedMountCapabilities{
			queryRuntimeInfo: func(context.Context, string) (*apitypes.RuntimeInfo, error) {
				calls++
				if calls == 1 {
					return nil, errdefs.ErrUnavailable
				}
				return &apitypes.RuntimeInfo{
					Annotations: map[string]string{deprecatedAllowedMounts: "erofs"},
				}, nil
			},
		}

		require.Nil(t, d.lookup(context.Background(), testRuntimeName))
		caps := d.lookup(context.Background(), testRuntimeName)
		require.NotNil(t, caps)
		assert.Equal(t, []string{"erofs"}, caps.Types)
		assert.Equal(t, 2, calls, "a failed query must be retried on the next lookup")
	})

	t.Run("the result is cached per runtime name", func(t *testing.T) {
		calls := 0
		d := &deprecatedMountCapabilities{
			queryRuntimeInfo: func(context.Context, string) (*apitypes.RuntimeInfo, error) {
				calls++
				return &apitypes.RuntimeInfo{
					Annotations: map[string]string{deprecatedAllowedMounts: "erofs"},
				}, nil
			},
		}

		first := d.lookup(context.Background(), testRuntimeName)
		second := d.lookup(context.Background(), testRuntimeName)
		require.NotNil(t, first)
		require.NotNil(t, second)
		assert.Equal(t, []string{"erofs"}, second.Types)
		assert.Equal(t, 1, calls, "runtime should only be queried once")
	})

	t.Run("a nil result is also cached", func(t *testing.T) {
		calls := 0
		d := &deprecatedMountCapabilities{
			queryRuntimeInfo: func(context.Context, string) (*apitypes.RuntimeInfo, error) {
				calls++
				return &apitypes.RuntimeInfo{}, nil
			},
		}

		assert.Nil(t, d.lookup(context.Background(), testRuntimeName))
		assert.Nil(t, d.lookup(context.Background(), testRuntimeName))
		assert.Equal(t, 1, calls, "a runtime with nothing to migrate should only be queried once")
	})

	t.Run("different runtimes are cached independently", func(t *testing.T) {
		queriedFor := map[string]int{}
		d := &deprecatedMountCapabilities{
			queryRuntimeInfo: func(_ context.Context, name string) (*apitypes.RuntimeInfo, error) {
				queriedFor[name]++
				return &apitypes.RuntimeInfo{
					Annotations: map[string]string{deprecatedAllowedMounts: "erofs"},
				}, nil
			},
		}

		d.lookup(context.Background(), "runtime-a")
		d.lookup(context.Background(), "runtime-b")
		d.lookup(context.Background(), "runtime-a")

		assert.Equal(t, 1, queriedFor["runtime-a"])
		assert.Equal(t, 1, queriedFor["runtime-b"])
	})
}

func TestDeprecatedParseAllowedMounts(t *testing.T) {
	for _, tc := range []struct {
		name               string
		value              string
		expectedTypes      []string
		expectedTransforms []string
	}{
		{
			name:          "single type",
			value:         "erofs",
			expectedTypes: []string{"erofs"},
		},
		{
			name:               "single transform",
			value:              "format/*",
			expectedTransforms: []string{"format"},
		},
		{
			name:               "mixed",
			value:              "block,format/*,mkfs/*",
			expectedTypes:      []string{"block"},
			expectedTransforms: []string{"format", "mkfs"},
		},
		{
			// A compound mount type keeps its inner slashes; only a
			// trailing "/*" marks a transform.
			name:          "compound type is not a transform",
			value:         "format/mkdir/overlay",
			expectedTypes: []string{"format/mkdir/overlay"},
		},
		{
			name:  "empty entries are skipped",
			value: "erofs,,loop",
			expectedTypes: []string{
				"erofs",
				"loop",
			},
		},
		{
			name: "empty value",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			caps := deprecatedParseAllowedMounts(tc.value)
			require.NotNil(t, caps)
			assert.Equal(t, tc.expectedTypes, caps.Types)
			assert.Equal(t, tc.expectedTransforms, caps.Transforms)
		})
	}
}
