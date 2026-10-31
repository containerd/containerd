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
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"

	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/containerd/containerd/v2/pkg/shim"
)

// TestStop runs Stop the way containerd runs "delete": cwd is the bundle,
// -bundle names it, and the bundle may already be gone.
func TestStop(t *testing.T) {
	for _, tc := range []struct {
		name       string
		removed    bool   // bundle removed before Stop
		noOpts     bool   // no shim.Opts in ctx: fall back to the cwd
		bundleTail string // appended to the -bundle value
	}{
		{name: "bundle_present"},
		{name: "bundle_removed", removed: true},
		{name: "bundle_present_no_opts", noOpts: true},
		{name: "bundle_present_trailing_slash", bundleTail: "/"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const id = "test-container"

			// A stand-in runc records its arguments, so the test needs
			// neither root nor a real runc and never touches /run.
			root := t.TempDir()
			bundle := filepath.Join(root, id)
			require.NoError(t, os.Mkdir(bundle, 0o700))
			require.NoError(t, os.WriteFile(filepath.Join(bundle, "init.pid"), []byte("1234"), 0o600))
			argsFile := filepath.Join(root, "args")
			require.NoError(t, os.WriteFile(filepath.Join(root, "runc"),
				[]byte("#!/bin/sh\necho \"$@\" > \"$RUNC_ARGS_FILE\"\n"), 0o700))
			t.Setenv("RUNC_ARGS_FILE", argsFile)
			t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))

			t.Chdir(bundle)
			if tc.removed {
				require.NoError(t, os.RemoveAll(bundle))
			}

			ctx := namespaces.WithNamespace(t.Context(), "k8s.io")
			if !tc.noOpts {
				ctx = context.WithValue(ctx, shim.OptsKey{}, shim.Opts{BundlePath: bundle + tc.bundleTail})
			}
			ss, err := manager{}.Stop(ctx, id)
			require.NoError(t, err)
			assert.Equal(t, 128+int(unix.SIGKILL), ss.ExitStatus)
			wantPid := 1234
			if tc.removed {
				wantPid = 0
			}
			assert.Equal(t, wantPid, ss.Pid)

			// runc must still be asked to delete the state, and must not be
			// given a --log path that may be inside a removed bundle.
			args, err := os.ReadFile(argsFile)
			require.NoError(t, err)
			assert.Contains(t, string(args), "delete --force "+id)
			assert.NotContains(t, string(args), "--log ")
		})
	}
}
