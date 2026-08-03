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

package fsmount_test

import (
	"fmt"
	"testing"

	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/containerd/v2/internal/fsmount"
	"github.com/containerd/containerd/v2/pkg/testutil"

	"github.com/opencontainers/selinux/go-selinux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test mounts with SELinux context specified, with and without fsconfig
func TestMountSelinuxContext(t *testing.T) {
	testutil.RequiresRoot(t)

	if !selinux.GetEnabled() {
		t.Skip("selinux is not enabled")
	}

	if !fsmount.SupportsFsmount() {
		t.Skip("fsmount is not available")
	}

	tmp := t.TempDir()

	// Reuse the label of tmp for the test so we don't depend on a specific policy
	label, err := selinux.FileLabel(tmp)
	require.NoError(t, err)

	ml := fmt.Sprintf("context=%q", label)

	mnt := mount.Mount{
		Type:    "tmpfs",
		Options: []string{"mode=0700", ml},
	}

	err = mnt.Mount(tmp)
	t.Cleanup(func() {
		testutil.Unmount(t, tmp)
	})
	require.NoError(t, err)

	newlabel, err := selinux.FileLabel(tmp)
	require.NoError(t, err)
	assert.Equal(t, label, newlabel)

	err = fsmount.Fsmount(mnt, tmp)
	require.NoError(t, err)

	newlabel, err = selinux.FileLabel(tmp)
	require.NoError(t, err)
	assert.Equal(t, label, newlabel)
}
