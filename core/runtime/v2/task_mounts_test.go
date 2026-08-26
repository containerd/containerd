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

	bootapi "github.com/containerd/containerd/api/runtime/bootstrap/v1"
	apitypes "github.com/containerd/containerd/api/types"
	"github.com/containerd/errdefs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/containerd/containerd/v2/core/mount"
)

// fakeMountManager is a mount.Manager whose behavior is set per test.
// It captures the ActivateOptions passed to the most recent Activate call so
// that tests can assert on the claims translated from a bootstrap result.
type fakeMountManager struct {
	activateErr   error
	activateAI    mount.ActivationInfo
	infoErr       error
	infoAI        mount.ActivationInfo
	deactivated   []string
	deactivateErr error

	lastActivateOpts mount.ActivateOptions
}

func (f *fakeMountManager) Activate(_ context.Context, _ string, _ []mount.Mount, opts ...mount.ActivateOpt) (mount.ActivationInfo, error) {
	var o mount.ActivateOptions
	for _, opt := range opts {
		opt(&o)
	}
	f.lastActivateOpts = o
	return f.activateAI, f.activateErr
}

func (f *fakeMountManager) Deactivate(_ context.Context, name string) error {
	f.deactivated = append(f.deactivated, name)
	return f.deactivateErr
}

func (f *fakeMountManager) Info(_ context.Context, _ string) (mount.ActivationInfo, error) {
	return f.infoAI, f.infoErr
}

func (f *fakeMountManager) Update(_ context.Context, ai mount.ActivationInfo, _ ...string) (mount.ActivationInfo, error) {
	return ai, nil
}

func (f *fakeMountManager) List(_ context.Context, _ ...string) ([]mount.ActivationInfo, error) {
	return nil, nil
}

var _ mount.Manager = (*fakeMountManager)(nil)

func mountManagementResult(t *testing.T, caps *apitypes.MountCapabilities) *bootapi.BootstrapResult {
	t.Helper()
	r := &bootapi.BootstrapResult{Version: 3}
	if caps != nil {
		require.NoError(t, r.AddExtension(caps))
	}
	return r
}

const testRuntimeName = "io.containerd.example.v1"

func TestTaskMountControllerActivate(t *testing.T) {
	rootfs := []mount.Mount{{Type: "bind", Source: "/src"}}

	t.Run("nil manager returns rootfs unchanged", func(t *testing.T) {
		c := &taskMountController{}
		activation, err := c.Activate(context.Background(), "task", testRuntimeName, nil, rootfs)
		require.NoError(t, err)
		assert.Equal(t, rootfs, activation.rootfs)
		assert.False(t, activation.owned)
	})

	t.Run("success is owned and returns system mounts", func(t *testing.T) {
		system := []mount.Mount{{Type: "overlay"}}
		fm := &fakeMountManager{activateAI: mount.ActivationInfo{System: system}}
		c := &taskMountController{manager: fm}

		activation, err := c.Activate(context.Background(), "task", testRuntimeName, nil, rootfs)
		require.NoError(t, err)
		assert.Equal(t, system, activation.rootfs)
		assert.True(t, activation.owned)
	})

	t.Run("already exists reuses the existing activation and is not owned", func(t *testing.T) {
		system := []mount.Mount{{Type: "overlay"}}
		fm := &fakeMountManager{
			activateErr: errdefs.ErrAlreadyExists,
			infoAI:      mount.ActivationInfo{System: system},
		}
		c := &taskMountController{manager: fm}

		activation, err := c.Activate(context.Background(), "task", testRuntimeName, nil, rootfs)
		require.NoError(t, err)
		assert.Equal(t, system, activation.rootfs)
		assert.False(t, activation.owned)
	})

	t.Run("already exists propagates an Info failure", func(t *testing.T) {
		fm := &fakeMountManager{
			activateErr: errdefs.ErrAlreadyExists,
			infoErr:     errdefs.ErrUnavailable,
		}
		c := &taskMountController{manager: fm}

		_, err := c.Activate(context.Background(), "task", testRuntimeName, nil, rootfs)
		require.Error(t, err)
	})

	t.Run("not implemented returns rootfs unchanged and is not owned", func(t *testing.T) {
		fm := &fakeMountManager{activateErr: errdefs.ErrNotImplemented}
		c := &taskMountController{manager: fm}

		activation, err := c.Activate(context.Background(), "task", testRuntimeName, nil, rootfs)
		require.NoError(t, err)
		assert.Equal(t, rootfs, activation.rootfs)
		assert.False(t, activation.owned)
	})

	t.Run("other errors are propagated", func(t *testing.T) {
		fm := &fakeMountManager{activateErr: errdefs.ErrUnavailable}
		c := &taskMountController{manager: fm}

		_, err := c.Activate(context.Background(), "task", testRuntimeName, nil, rootfs)
		require.Error(t, err)
	})

	t.Run("claims from the bootstrap extension are translated into options", func(t *testing.T) {
		fm := &fakeMountManager{}
		c := &taskMountController{manager: fm}

		bootstrap := mountManagementResult(t, &apitypes.MountCapabilities{
			Types:      []string{"erofs", "loop"},
			Transforms: []string{"format", "mkfs"},
		})

		_, err := c.Activate(context.Background(), "task", testRuntimeName, bootstrap, rootfs)
		require.NoError(t, err)
		assert.Equal(t, []string{"erofs", "loop"}, fm.lastActivateOpts.AllowMountTypes)
		assert.Equal(t, []string{"format", "mkfs"}, fm.lastActivateOpts.AllowTransforms)
	})

	t.Run("no extension claims nothing", func(t *testing.T) {
		fm := &fakeMountManager{}
		c := &taskMountController{manager: fm}

		bootstrap := mountManagementResult(t, nil)

		_, err := c.Activate(context.Background(), "task", testRuntimeName, bootstrap, rootfs)
		require.NoError(t, err)
		assert.Empty(t, fm.lastActivateOpts.AllowMountTypes)
		assert.Empty(t, fm.lastActivateOpts.AllowTransforms)
	})

	t.Run("a malformed extension claims nothing rather than failing", func(t *testing.T) {
		fm := &fakeMountManager{}
		c := &taskMountController{manager: fm}

		bootstrap := &bootapi.BootstrapResult{
			Version: 3,
			Extensions: []*bootapi.Extension{{
				Value: &anypb.Any{
					TypeUrl: "type.googleapis.com/containerd.types.MountCapabilities",
					Value:   []byte{0xff, 0xff, 0xff},
				},
			}},
		}

		_, err := c.Activate(context.Background(), "task", testRuntimeName, bootstrap, rootfs)
		require.NoError(t, err)
		assert.Empty(t, fm.lastActivateOpts.AllowMountTypes)
		assert.Empty(t, fm.lastActivateOpts.AllowTransforms)
	})

	t.Run("extension present takes precedence, legacy is not consulted", func(t *testing.T) {
		fm := &fakeMountManager{}
		legacyCalled := false
		c := &taskMountController{
			manager: fm,
			legacy: &deprecatedMountCapabilities{
				queryRuntimeInfo: func(context.Context, string) (*apitypes.RuntimeInfo, error) {
					legacyCalled = true
					return &apitypes.RuntimeInfo{}, nil
				},
			},
		}

		bootstrap := mountManagementResult(t, &apitypes.MountCapabilities{Types: []string{"erofs"}})

		_, err := c.Activate(context.Background(), "task", testRuntimeName, bootstrap, rootfs)
		require.NoError(t, err)
		assert.Equal(t, []string{"erofs"}, fm.lastActivateOpts.AllowMountTypes)
		assert.False(t, legacyCalled, "legacy annotation lookup must not run when the extension is present")
	})

	t.Run("no extension falls back to the deprecated annotation", func(t *testing.T) {
		fm := &fakeMountManager{}
		c := &taskMountController{
			manager: fm,
			legacy: &deprecatedMountCapabilities{
				queryRuntimeInfo: func(context.Context, string) (*apitypes.RuntimeInfo, error) {
					return &apitypes.RuntimeInfo{
						Annotations: map[string]string{
							deprecatedAllowedMounts: "block,format/*",
						},
					}, nil
				},
			},
		}

		_, err := c.Activate(context.Background(), "task", testRuntimeName, nil, rootfs)
		require.NoError(t, err)
		assert.Equal(t, []string{"block"}, fm.lastActivateOpts.AllowMountTypes)
		assert.Equal(t, []string{"format"}, fm.lastActivateOpts.AllowTransforms)
	})

	t.Run("no extension and no annotation claims nothing", func(t *testing.T) {
		fm := &fakeMountManager{}
		c := &taskMountController{
			manager: fm,
			legacy: &deprecatedMountCapabilities{
				queryRuntimeInfo: func(context.Context, string) (*apitypes.RuntimeInfo, error) {
					return &apitypes.RuntimeInfo{}, nil
				},
			},
		}

		_, err := c.Activate(context.Background(), "task", testRuntimeName, nil, rootfs)
		require.NoError(t, err)
		assert.Empty(t, fm.lastActivateOpts.AllowMountTypes)
		assert.Empty(t, fm.lastActivateOpts.AllowTransforms)
	})

	t.Run("a failed legacy lookup claims nothing rather than failing", func(t *testing.T) {
		fm := &fakeMountManager{}
		c := &taskMountController{
			manager: fm,
			legacy: &deprecatedMountCapabilities{
				queryRuntimeInfo: func(context.Context, string) (*apitypes.RuntimeInfo, error) {
					return nil, errdefs.ErrUnavailable
				},
			},
		}

		_, err := c.Activate(context.Background(), "task", testRuntimeName, nil, rootfs)
		require.NoError(t, err)
		assert.Empty(t, fm.lastActivateOpts.AllowMountTypes)
	})

	t.Run("well known default runtimes are never queried for the annotation", func(t *testing.T) {
		fm := &fakeMountManager{}
		queried := false
		c := &taskMountController{
			manager: fm,
			legacy: &deprecatedMountCapabilities{
				queryRuntimeInfo: func(context.Context, string) (*apitypes.RuntimeInfo, error) {
					queried = true
					return &apitypes.RuntimeInfo{}, nil
				},
			},
		}

		_, err := c.Activate(context.Background(), "task", "io.containerd.runc.v2", nil, rootfs)
		require.NoError(t, err)
		assert.False(t, queried)
	})
}

func TestTaskMountControllerDeactivate(t *testing.T) {
	t.Run("nil manager is a no-op", func(t *testing.T) {
		c := &taskMountController{}
		require.NoError(t, c.Deactivate(context.Background(), "task"))
	})

	t.Run("deactivates by name", func(t *testing.T) {
		fm := &fakeMountManager{}
		c := &taskMountController{manager: fm}

		require.NoError(t, c.Deactivate(context.Background(), "task"))
		assert.Equal(t, []string{"task"}, fm.deactivated)
	})
}
