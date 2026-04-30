//go:build linux && integration

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

package lcc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestFloxHelloWorkflow exercises the flox build path end-to-end:
//
//  1. Build a LCC OCI image from the flox/hello FloxHub environment using pkg2oci.
//  2. Import the image via ctr (triggers unpack through the snapshotter).
//  3. Run the container and verify the GNU hello output.
//  4. Prune and verify all snapshots and LCC cache dirs are removed.
func TestFloxHelloWorkflow(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("requires root (overlayfs + containerd)")
	}
	checkBinaries(t, "containerd", "ctr", "pkg2oci", "flox")

	env := setupEnv(t)
	const ref = "localhost/test/flox-hello:v1"
	tarPath := filepath.Join(env.tmp, "flox-hello.tar")

	runCmd(t, env.ctx, "pkg2oci",
		"--output", tarPath,
		"--flox-env", "flox/hello",
		"--entrypoint", "hello",
		ref,
	)
	t.Logf("pkg2oci: wrote %s", tarPath)

	ctrImport(t, env, tarPath)

	cacheDir := env.cacheDir
	blobCount := countCachedBlobs(t, cacheDir)
	if blobCount == 0 {
		t.Error("expected at least one LCC cache directory")
	}
	t.Logf("LCC cache dirs (Nix store paths): %d", blobCount)

	out := ctrRun(t, env, ref, "flox-hello-run")
	t.Logf("flox/hello output: %q", out)
	if !strings.Contains(out, "Hello, world!") {
		t.Errorf("expected 'Hello, world!' in output, got: %q", out)
	}

	pruneAll(t, env, ref)
}
