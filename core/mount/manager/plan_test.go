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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/containerd/containerd/v2/core/mount"
)

// fakeTransformer is a no-op mount.Transformer, standing in for format,
// mkfs and mkdir in tests that only care about which transforms are applied,
// not what they do.
type fakeTransformer struct{}

func (fakeTransformer) Transform(_ context.Context, m mount.Mount, _ []mount.ActiveMount) (mount.Mount, error) {
	return m, nil
}

// fakeHandler is a no-op mount.Handler.
type fakeHandler struct{}

func (fakeHandler) Mount(context.Context, mount.Mount, string, []mount.ActiveMount) (mount.ActiveMount, error) {
	return mount.ActiveMount{}, nil
}

func (fakeHandler) Unmount(context.Context, string) error { return nil }

var testTransforms = map[string]mount.Transformer{
	"format": fakeTransformer{},
	"mkfs":   fakeTransformer{},
	"mkdir":  fakeTransformer{},
}

func TestPlanActivationNoTransformsOrHandlers(t *testing.T) {
	mounts := []mount.Mount{{Type: "bind"}}
	plan := planActivation(context.Background(), mounts, mount.ActivateOptions{}, testTransforms, nil)
	assert.Equal(t, -1, plan.firstSystemMount)
	assert.Nil(t, plan.transforms)
	assert.Nil(t, plan.handlers)
}

func TestPlanActivationTransformChain(t *testing.T) {
	mounts := []mount.Mount{{Type: "format/mkdir/overlay"}}

	for _, tc := range []struct {
		name              string
		opts              []mount.ActivateOpt
		expectFirstSystem int
	}{
		{
			name:              "nothing claimed requires the manager",
			expectFirstSystem: 0,
		},
		{
			name:              "a claimed transform still requires the manager for now",
			opts:              []mount.ActivateOpt{mount.WithAllowTransform("mkdir")},
			expectFirstSystem: 0,
		},
		{
			name: "every transform claimed no longer requires the manager",
			opts: []mount.ActivateOpt{
				mount.WithAllowTransform("format"),
				mount.WithAllowTransform("mkdir"),
			},
			expectFirstSystem: -1,
		},
		{
			// Claiming the full literal mount type is equivalent to
			// claiming every transform in its chain.
			name:              "full mount type claimed no longer requires the manager",
			opts:              []mount.ActivateOpt{mount.WithAllowMountType("format/mkdir/overlay")},
			expectFirstSystem: -1,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var config mount.ActivateOptions
			for _, opt := range tc.opts {
				opt(&config)
			}

			plan := planActivation(context.Background(), mounts, config, testTransforms, nil)
			assert.Equal(t, tc.expectFirstSystem, plan.firstSystemMount)
			require.Len(t, plan.transforms[0], 2)
		})
	}
}

func TestPlanActivationUnknownTransform(t *testing.T) {
	mounts := []mount.Mount{{Type: "unknown/overlay"}}
	plan := planActivation(context.Background(), mounts, mount.ActivateOptions{}, testTransforms, nil)
	// The unrecognized prefix is left as part of the base mount type; with
	// no handler registered for it, nothing needs the manager.
	assert.Equal(t, -1, plan.firstSystemMount)
}

func TestPlanActivationHandler(t *testing.T) {
	handlers := map[string]mount.Handler{"erofs": fakeHandler{}}

	t.Run("unclaimed handled type advances firstSystemMount past it", func(t *testing.T) {
		mounts := []mount.Mount{{Type: "erofs"}, {Type: "bind"}}
		plan := planActivation(context.Background(), mounts, mount.ActivateOptions{}, testTransforms, handlers)
		assert.Equal(t, 1, plan.firstSystemMount)
		require.Len(t, plan.handlers, 2)
		assert.NotNil(t, plan.handlers[0])
	})

	t.Run("claimed handled type keeps its handler but does not advance firstSystemMount", func(t *testing.T) {
		mounts := []mount.Mount{{Type: "erofs"}}
		config := mount.ActivateOptions{}
		mount.WithAllowMountType("erofs")(&config)

		plan := planActivation(context.Background(), mounts, config, testTransforms, handlers)
		assert.Equal(t, -1, plan.firstSystemMount)
		require.Len(t, plan.handlers, 1)
		assert.NotNil(t, plan.handlers[0], "the handler is still recorded, in case a later mount needs it applied")
	})

	t.Run("a later unclaimed handled mount still requires everything before it", func(t *testing.T) {
		handlers := map[string]mount.Handler{"erofs": fakeHandler{}, "loop": fakeHandler{}}
		mounts := []mount.Mount{{Type: "erofs"}, {Type: "loop"}, {Type: "overlay"}}
		config := mount.ActivateOptions{}
		mount.WithAllowMountType("erofs")(&config)
		// "loop" is left unclaimed.

		plan := planActivation(context.Background(), mounts, config, testTransforms, handlers)
		// Nothing claims "loop", so the manager must perform it, and
		// therefore everything before it too, including the claimed erofs
		// mount, using the handler recorded for it.
		assert.Equal(t, 2, plan.firstSystemMount)
		require.Len(t, plan.handlers, 3)
		assert.NotNil(t, plan.handlers[0], "erofs's handler must still be applied despite being claimed")
	})

	t.Run("a later unclaimed transform still requires an earlier claimed handled mount", func(t *testing.T) {
		mounts := []mount.Mount{{Type: "erofs"}, {Type: "format/overlay"}}
		config := mount.ActivateOptions{}
		mount.WithAllowMountType("erofs")(&config)
		// "format" is left unclaimed.

		plan := planActivation(context.Background(), mounts, config, testTransforms, handlers)
		// The unclaimed transform on the second mount reaches back the same
		// way an unclaimed handled type does: through firstSystemMount, not
		// through anything specific to handlers.
		assert.Equal(t, 1, plan.firstSystemMount)
	})
}

func TestPlanActivationTemporary(t *testing.T) {
	mounts := []mount.Mount{{Type: "bind"}}
	config := mount.ActivateOptions{}
	mount.WithTemporary(&config)

	plan := planActivation(context.Background(), mounts, config, testTransforms, nil)
	require.Len(t, plan.handlers, 1)
	assert.Equal(t, 1, plan.firstSystemMount)
}
