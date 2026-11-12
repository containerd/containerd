//go:build linux

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

package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/containerd/containerd/v2/plugins"
)

func TestManagerName(t *testing.T) {
	assert.Equal(t, plugins.RuntimeRuncV2, newManager().Name())
}

func TestStopCleansUpTheBundle(t *testing.T) {
	// "shim delete" in a sandbox bundle removes what the sandbox created and
	// leaves what containerd created. The runc task cleanup that runs first
	// finds no container there and only logs about it.
	root := t.TempDir()
	const id = "sandbox-id"
	bundle := filepath.Join(root, id)
	require.NoError(t, os.MkdirAll(filepath.Join(bundle, "ns"), 0o700))
	require.NoError(t, os.Mkdir(filepath.Join(bundle, "rootfs"), 0o700))
	for _, f := range []string{"hostname", "hosts", "resolv.conf", filepath.Join("ns", "ipc"), filepath.Join("ns", "uts")} {
		require.NoError(t, os.WriteFile(filepath.Join(bundle, f), nil, 0o600))
	}
	t.Chdir(bundle)

	_, err := newManager().Stop(namespaces.WithNamespace(context.Background(), "test"), id)
	require.NoError(t, err)

	for _, f := range []string{"hostname", "hosts", "resolv.conf", "ns"} {
		_, err := os.Stat(filepath.Join(bundle, f))
		assert.True(t, os.IsNotExist(err), "%s should be gone", f)
	}
	_, err = os.Stat(filepath.Join(bundle, "rootfs"))
	assert.NoError(t, err, "what containerd created is left alone")
}
