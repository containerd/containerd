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
	"sync"
	"sync/atomic"
	"testing"

	apitypes "github.com/containerd/containerd/api/types"
	"github.com/containerd/errdefs"
	"github.com/containerd/typeurl/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/anypb"
)

func TestDeprecatedMountCapabilitiesLookup(t *testing.T) {
	t.Run("nil receiver returns nil", func(t *testing.T) {
		var d *deprecatedMountCapabilities
		assert.Nil(t, d.lookup(context.Background(), testRuntimeName, nil))
	})

	t.Run("well known default runtimes are never queried", func(t *testing.T) {
		for _, name := range []string{"io.containerd.runc.v2", "io.containerd.runhcs.v1"} {
			queried := false
			d := &deprecatedMountCapabilities{
				queryRuntimeInfo: func(context.Context, string, typeurl.Any) (*apitypes.RuntimeInfo, error) {
					queried = true
					return &apitypes.RuntimeInfo{}, nil
				},
			}
			assert.Nil(t, d.lookup(context.Background(), name, nil))
			assert.False(t, queried, "must not query %q", name)
		}
	})

	t.Run("annotation present is parsed", func(t *testing.T) {
		d := &deprecatedMountCapabilities{
			queryRuntimeInfo: func(context.Context, string, typeurl.Any) (*apitypes.RuntimeInfo, error) {
				return &apitypes.RuntimeInfo{
					Annotations: map[string]string{
						deprecatedAllowedMounts: "erofs,format/*",
					},
				}, nil
			},
		}

		caps := d.lookup(context.Background(), testRuntimeName, nil)
		require.NotNil(t, caps)
		assert.Equal(t, []string{"erofs"}, caps.Types)
		assert.Equal(t, []string{"format"}, caps.Transforms)
	})

	t.Run("annotation absent returns nil", func(t *testing.T) {
		d := &deprecatedMountCapabilities{
			queryRuntimeInfo: func(context.Context, string, typeurl.Any) (*apitypes.RuntimeInfo, error) {
				return &apitypes.RuntimeInfo{}, nil
			},
		}
		assert.Nil(t, d.lookup(context.Background(), testRuntimeName, nil))
	})

	t.Run("a query error returns nil rather than failing", func(t *testing.T) {
		d := &deprecatedMountCapabilities{
			queryRuntimeInfo: func(context.Context, string, typeurl.Any) (*apitypes.RuntimeInfo, error) {
				return nil, errdefs.ErrUnavailable
			},
		}
		assert.Nil(t, d.lookup(context.Background(), testRuntimeName, nil))
	})

	// A transient failure (a momentary fork failure, for example) must not be
	// cached as a permanent "nothing to migrate" result: that would silently
	// and permanently disable the migration path for that runtime for the
	// life of the daemon, unlike the loadShimInfo mechanism this replaced,
	// which retried on the next Create.
	t.Run("a query error is not cached and is retried", func(t *testing.T) {
		calls := 0
		d := &deprecatedMountCapabilities{
			queryRuntimeInfo: func(context.Context, string, typeurl.Any) (*apitypes.RuntimeInfo, error) {
				calls++
				if calls == 1 {
					return nil, errdefs.ErrUnavailable
				}
				return &apitypes.RuntimeInfo{
					Annotations: map[string]string{deprecatedAllowedMounts: "erofs"},
				}, nil
			},
		}

		require.Nil(t, d.lookup(context.Background(), testRuntimeName, nil))
		caps := d.lookup(context.Background(), testRuntimeName, nil)
		require.NotNil(t, caps)
		assert.Equal(t, []string{"erofs"}, caps.Types)
		assert.Equal(t, 2, calls, "a failed query must be retried on the next lookup")
	})

	t.Run("the result is cached per runtime name", func(t *testing.T) {
		calls := 0
		d := &deprecatedMountCapabilities{
			queryRuntimeInfo: func(context.Context, string, typeurl.Any) (*apitypes.RuntimeInfo, error) {
				calls++
				return &apitypes.RuntimeInfo{
					Annotations: map[string]string{deprecatedAllowedMounts: "erofs"},
				}, nil
			},
		}

		first := d.lookup(context.Background(), testRuntimeName, nil)
		second := d.lookup(context.Background(), testRuntimeName, nil)
		require.NotNil(t, first)
		require.NotNil(t, second)
		assert.Equal(t, []string{"erofs"}, second.Types)
		assert.Equal(t, 1, calls, "runtime should only be queried once")
	})

	t.Run("a nil result is also cached", func(t *testing.T) {
		calls := 0
		d := &deprecatedMountCapabilities{
			queryRuntimeInfo: func(context.Context, string, typeurl.Any) (*apitypes.RuntimeInfo, error) {
				calls++
				return &apitypes.RuntimeInfo{}, nil
			},
		}

		assert.Nil(t, d.lookup(context.Background(), testRuntimeName, nil))
		assert.Nil(t, d.lookup(context.Background(), testRuntimeName, nil))
		assert.Equal(t, 1, calls, "a runtime with nothing to migrate should only be queried once")
	})

	t.Run("different runtimes are cached independently", func(t *testing.T) {
		queriedFor := map[string]int{}
		d := &deprecatedMountCapabilities{
			queryRuntimeInfo: func(_ context.Context, name string, _ typeurl.Any) (*apitypes.RuntimeInfo, error) {
				queriedFor[name]++
				return &apitypes.RuntimeInfo{
					Annotations: map[string]string{deprecatedAllowedMounts: "erofs"},
				}, nil
			},
		}

		d.lookup(context.Background(), "runtime-a", nil)
		d.lookup(context.Background(), "runtime-b", nil)
		d.lookup(context.Background(), "runtime-a", nil)

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

func TestDeprecatedMountCapabilitiesOptions(t *testing.T) {
	ctx := context.Background()
	t.Run("values and types are cached independently", func(t *testing.T) {
		var calls atomic.Int32
		d := &deprecatedMountCapabilities{queryRuntimeInfo: func(_ context.Context, _ string, options typeurl.Any) (*apitypes.RuntimeInfo, error) {
			calls.Add(1)
			return &apitypes.RuntimeInfo{Annotations: map[string]string{deprecatedAllowedMounts: options.GetTypeUrl() + "/" + string(options.GetValue())}}, nil
		}}
		variants := []*anypb.Any{
			{TypeUrl: "options-a", Value: []byte("first")},
			{TypeUrl: "options-a", Value: []byte("second")},
			{TypeUrl: "options-b", Value: []byte("first")},
		}
		check := func(options *anypb.Any) {
			caps := d.lookup(ctx, testRuntimeName, options)
			if assert.NotNil(t, caps) {
				assert.Equal(t, []string{options.TypeUrl + "/" + string(options.Value)}, caps.Types)
			}
		}
		for _, options := range variants {
			check(options)
		}
		var wg sync.WaitGroup
		for i := 0; i < 32; i++ {
			for _, options := range variants {
				wg.Go(func() { check(options) })
			}
		}
		wg.Wait()
		assert.Equal(t, int32(len(variants)), calls.Load())
	})
	t.Run("nil result does not hide another configuration", func(t *testing.T) {
		calls := 0
		d := &deprecatedMountCapabilities{queryRuntimeInfo: func(_ context.Context, _ string, options typeurl.Any) (*apitypes.RuntimeInfo, error) {
			calls++
			if options == nil {
				return &apitypes.RuntimeInfo{}, nil
			}
			return &apitypes.RuntimeInfo{Annotations: map[string]string{deprecatedAllowedMounts: "erofs"}}, nil
		}}
		require.Nil(t, d.lookup(ctx, testRuntimeName, nil))
		configured := &anypb.Any{TypeUrl: "options", Value: []byte("configured")}
		require.NotNil(t, d.lookup(ctx, testRuntimeName, configured))
		assert.Nil(t, d.lookup(ctx, testRuntimeName, nil))
		assert.NotNil(t, d.lookup(ctx, testRuntimeName, configured))
		assert.Equal(t, 2, calls)
	})
	t.Run("cached keys do not alias caller buffers", func(t *testing.T) {
		calls := 0
		d := &deprecatedMountCapabilities{queryRuntimeInfo: func(_ context.Context, _ string, options typeurl.Any) (*apitypes.RuntimeInfo, error) {
			calls++
			return &apitypes.RuntimeInfo{Annotations: map[string]string{deprecatedAllowedMounts: string(options.GetValue())}}, nil
		}}
		options := &anypb.Any{TypeUrl: "options", Value: []byte("a")}
		require.Equal(t, []string{"a"}, d.lookup(ctx, testRuntimeName, options).Types)
		options.Value[0] = 'b'
		require.Equal(t, []string{"b"}, d.lookup(ctx, testRuntimeName, options).Types)
		require.Equal(t, []string{"a"}, d.lookup(ctx, testRuntimeName, &anypb.Any{TypeUrl: "options", Value: []byte("a")}).Types)
		assert.Equal(t, 2, calls)
	})
	t.Run("failed configuration is retried independently", func(t *testing.T) {
		attempts := map[string]int{}
		d := &deprecatedMountCapabilities{queryRuntimeInfo: func(_ context.Context, _ string, options typeurl.Any) (*apitypes.RuntimeInfo, error) {
			key := string(options.GetValue())
			attempts[key]++
			if key == "second" && attempts[key] == 1 {
				return nil, errdefs.ErrUnavailable
			}
			return &apitypes.RuntimeInfo{Annotations: map[string]string{deprecatedAllowedMounts: key}}, nil
		}}
		first := &anypb.Any{TypeUrl: "options", Value: []byte("first")}
		second := &anypb.Any{TypeUrl: "options", Value: []byte("second")}
		require.NotNil(t, d.lookup(ctx, testRuntimeName, first))
		require.Nil(t, d.lookup(ctx, testRuntimeName, second))
		require.NotNil(t, d.lookup(ctx, testRuntimeName, first))
		require.Equal(t, []string{"second"}, d.lookup(ctx, testRuntimeName, second).Types)
		assert.Equal(t, map[string]int{"first": 1, "second": 2}, attempts)
	})
}
