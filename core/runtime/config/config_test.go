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

package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoad(t *testing.T) {
	t.Run("an empty path loads no runtimes", func(t *testing.T) {
		runtimes, err := Load[Runtime]("")

		require.NoError(t, err)
		assert.Empty(t, runtimes)
	})

	t.Run("a missing directory loads no runtimes", func(t *testing.T) {
		runtimes, err := Load[Runtime](filepath.Join(t.TempDir(), "missing"))

		require.NoError(t, err)
		assert.Empty(t, runtimes)
	})

	t.Run("runtime directories are loaded by handler name", func(t *testing.T) {
		dir := t.TempDir()
		writeRuntimeConfig(t, dir, "runc", `
runtime_type = "io.containerd.runc.v2"
runtime_path = "/usr/local/bin/containerd-shim-runc-v2"
snapshotter = "overlayfs"
pod_annotations = ["example.com/ignored"]

[options]
  BinaryName = "/usr/local/bin/runc"
`)
		writeRuntimeConfig(t, dir, "runhcs", `
runtime_type = "io.containerd.runhcs.v1"
`)

		runtimes, err := Load[Runtime](dir)

		require.NoError(t, err)
		require.Len(t, runtimes, 2)
		assert.Equal(t, Runtime{
			Type:        "io.containerd.runc.v2",
			Path:        "/usr/local/bin/containerd-shim-runc-v2",
			Snapshotter: "overlayfs",
			Options: map[string]any{
				"BinaryName": "/usr/local/bin/runc",
			},
		}, runtimes["runc"])
		assert.Equal(t, "io.containerd.runhcs.v1", runtimes["runhcs"].Type)
	})

	t.Run("consumer-specific fields are decoded into a superset", func(t *testing.T) {
		type criRuntime struct {
			Type           string   `toml:"runtime_type"`
			PodAnnotations []string `toml:"pod_annotations"`
		}

		dir := t.TempDir()
		writeRuntimeConfig(t, dir, "runc", `
runtime_type = "io.containerd.runc.v2"
pod_annotations = ["example.com/pod"]
`)

		runtimes, err := Load[criRuntime](dir)

		require.NoError(t, err)
		assert.Equal(t, criRuntime{
			Type:           "io.containerd.runc.v2",
			PodAnnotations: []string{"example.com/pod"},
		}, runtimes["runc"])
	})

	t.Run("entries without runtime config files are ignored", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "not-a-directory"), nil, 0o600))
		require.NoError(t, os.Mkdir(filepath.Join(dir, "missing-config"), 0o700))

		runtimes, err := Load[Runtime](dir)

		require.NoError(t, err)
		assert.Empty(t, runtimes)
	})

	t.Run("an unreadable runtime config returns an error", func(t *testing.T) {
		dir := t.TempDir()
		runtimeDir := filepath.Join(dir, "runc")
		require.NoError(t, os.Mkdir(runtimeDir, 0o700))
		require.NoError(t, os.Mkdir(filepath.Join(runtimeDir, configFile), 0o700))

		_, err := Load[Runtime](dir)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to read runtime config for \"runc\"")
	})

	t.Run("invalid runtime config returns an error", func(t *testing.T) {
		dir := t.TempDir()
		writeRuntimeConfig(t, dir, "runc", "{{{not toml")

		_, err := Load[Runtime](dir)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to decode runtime config for \"runc\"")
	})
}

func writeRuntimeConfig(t *testing.T, dir, handler, contents string) {
	t.Helper()

	runtimeDir := filepath.Join(dir, handler)
	require.NoError(t, os.Mkdir(runtimeDir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(runtimeDir, configFile), []byte(contents), 0o600))
}
