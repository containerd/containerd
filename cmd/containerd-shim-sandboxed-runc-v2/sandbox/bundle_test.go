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

package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	specs "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
	criruntime "k8s.io/cri-api/pkg/apis/runtime/v1"

	"github.com/containerd/containerd/v2/pkg/testutil"
)

func TestSetupFiles(t *testing.T) {
	testutil.RequiresRoot(t)
	ctx := context.Background()
	bundle := t.TempDir()
	t.Cleanup(func() { _ = Cleanup(ctx, bundle) })

	cfg := &Config{
		ID:       testID,
		Hostname: "pod-hostname",
		DNS: &criruntime.DNSConfig{
			Servers:  []string{"10.0.0.10"},
			Searches: []string{"svc.cluster.local"},
			Options:  []string{"ndots:5"},
		},
		ShmSize: 1 << 20,
	}
	mounts, err := setupFiles(bundle, cfg)
	require.NoError(t, err)

	byDestination := make(map[string]specs.Mount)
	for _, m := range mounts {
		byDestination[m.Destination] = m
	}
	require.Len(t, byDestination, 4)

	content, err := os.ReadFile(byDestination["/etc/hostname"].Source)
	require.NoError(t, err)
	assert.Equal(t, "pod-hostname\n", string(content))

	content, err = os.ReadFile(byDestination["/etc/resolv.conf"].Source)
	require.NoError(t, err)
	assert.Equal(t, "search svc.cluster.local\nnameserver 10.0.0.10\noptions ndots:5\n", string(content))

	hostHosts, err := os.ReadFile("/etc/hosts")
	require.NoError(t, err)
	content, err = os.ReadFile(byDestination["/etc/hosts"].Source)
	require.NoError(t, err)
	assert.Equal(t, string(hostHosts), string(content), "the pod hosts file starts as a copy of the host one")

	var st unix.Statfs_t
	require.NoError(t, unix.Statfs(byDestination["/dev/shm"].Source, &st))
	assert.EqualValues(t, unix.TMPFS_MAGIC, st.Type, "the pod shm is a tmpfs")
	assert.EqualValues(t, 1<<20, st.Blocks*uint64(st.Bsize), "sized as requested")

	require.NoError(t, Cleanup(ctx, bundle))
	assertNoMountsUnder(t, bundle)
	entries, err := os.ReadDir(bundle)
	require.NoError(t, err)
	assert.Empty(t, entries)

	// With the host IPC namespace the pod uses the host /dev/shm: no tmpfs.
	cfg.HostIPC = true
	cfg.DNS = nil
	mounts, err = setupFiles(bundle, cfg)
	require.NoError(t, err)
	require.Len(t, mounts, 3)
	for _, m := range mounts {
		assert.NotEqual(t, "/dev/shm", m.Destination)
	}
	hostResolv, err := os.ReadFile("/etc/resolv.conf")
	require.NoError(t, err)
	content, err = os.ReadFile(filepath.Join(bundle, "resolv.conf"))
	require.NoError(t, err)
	assert.Equal(t, string(hostResolv), string(content), "no DNS config means the host resolver")
}

func TestCleanupWithoutMounts(t *testing.T) {
	ctx := context.Background()
	bundle := t.TempDir()

	// Leftovers of a sandbox whose mounts are already gone (or that crashed
	// before mounting anything) are removed, and nothing else is touched.
	require.NoError(t, os.Mkdir(filepath.Join(bundle, pinDir), 0o700))
	require.NoError(t, os.Mkdir(filepath.Join(bundle, "shm"), 0o700))
	require.NoError(t, os.Mkdir(filepath.Join(bundle, "rootfs"), 0o700))
	for _, f := range []string{"hostname", "hosts", "resolv.conf", "ns/ipc", "ns/uts", "address"} {
		require.NoError(t, os.WriteFile(filepath.Join(bundle, f), []byte("x"), 0o600))
	}

	require.NoError(t, Cleanup(ctx, bundle))

	entries, err := os.ReadDir(bundle)
	require.NoError(t, err)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	assert.ElementsMatch(t, []string{"address", "rootfs"}, names, "only what containerd owns is left")

	// Idempotent, including on a bundle that no longer exists.
	require.NoError(t, Cleanup(ctx, bundle))
	require.NoError(t, Cleanup(ctx, filepath.Join(bundle, "does-not-exist")))
}
