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

package containerd

import (
	"bytes"
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/containerd/containerd/oci"
	"github.com/containerd/containerd/server"
	"github.com/containerd/containerd/testutil"
)

// the following nolint is for shutting up gometalinter on non-linux.
// nolint: unused
func newDaemonWithConfig(t *testing.T, configTOML string) (*Client, *daemon, func()) {
	if testing.Short() {
		t.Skip()
	}
	testutil.RequiresRoot(t)
	var (
		ctrd              = daemon{}
		configTOMLDecoded server.Config
		buf               = bytes.NewBuffer(nil)
	)

	tempDir, err := ioutil.TempDir("", "containerd-test-new-daemon-with-config")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err != nil {
			os.RemoveAll(tempDir)
		}
	}()

	configTOMLFile := filepath.Join(tempDir, "config.toml")
	if err = ioutil.WriteFile(configTOMLFile, []byte(configTOML), 0600); err != nil {
		t.Fatal(err)
	}

	if err = server.LoadConfig(configTOMLFile, &configTOMLDecoded); err != nil {
		t.Fatal(err)
	}

	address := configTOMLDecoded.GRPC.Address
	if address == "" {
		address = filepath.Join(tempDir, "containerd.sock")
	}
	args := []string{"-c", configTOMLFile}
	if configTOMLDecoded.Root == "" {
		args = append(args, "--root", filepath.Join(tempDir, "root"))
	}
	if configTOMLDecoded.State == "" {
		args = append(args, "--state", filepath.Join(tempDir, "state"))
	}
	if err = ctrd.start("containerd", address, args, buf, buf); err != nil {
		t.Fatalf("%v: %s", err, buf.String())
	}

	waitCtx, waitCancel := context.WithTimeout(context.TODO(), 2*time.Second)
	client, err := ctrd.waitForStart(waitCtx)
	waitCancel()
	if err != nil {
		ctrd.Kill()
		ctrd.Wait()
		t.Fatalf("%v: %s", err, buf.String())
	}

	cleanup := func() {
		if err := client.Close(); err != nil {
			t.Fatalf("failed to close client: %v", err)
		}
		if err := ctrd.Stop(); err != nil {
			if err := ctrd.Kill(); err != nil {
				t.Fatalf("failed to signal containerd: %v", err)
			}
		}
		if err := ctrd.Wait(); err != nil {
			if _, ok := err.(*exec.ExitError); !ok {
				t.Fatalf("failed to wait for: %v", err)
			}
		}
		if err := os.RemoveAll(tempDir); err != nil {
			t.Fatalf("failed to remove %s: %v", tempDir, err)
		}
		// cleaning config-specific resources is up to the caller
	}
	return client, &ctrd, cleanup

}

func testDaemonRuntimeRoot(t *testing.T, noShim bool) {
	runtimeRoot, err := ioutil.TempDir("", "containerd-test-runtime-root")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err != nil {
			os.RemoveAll(runtimeRoot)
		}
	}()
	configTOML := fmt.Sprintf(`
[plugins]
 [plugins.linux]
   no_shim = %v
   runtime_root = "%s"
`, noShim, runtimeRoot)

	client, _, cleanup := newDaemonWithConfig(t, configTOML)
	defer cleanup()

	ctx, cancel := testContext()
	defer cancel()
	// FIXME(AkihiroSuda): import locally frozen image?
	image, err := client.Pull(ctx, testImage, WithPullUnpack)
	if err != nil {
		t.Fatal(err)
	}

	id := t.Name()
	container, err := client.NewContainer(ctx, id, WithNewSpec(oci.WithImageConfig(image), withProcessArgs("top")), WithNewSnapshot(id, image))
	if err != nil {
		t.Fatal(err)
	}
	defer container.Delete(ctx, WithSnapshotCleanup)

	task, err := container.NewTask(ctx, empty())
	if err != nil {
		t.Fatal(err)
	}
	defer task.Delete(ctx)

	if err = task.Start(ctx); err != nil {
		t.Fatal(err)
	}

	stateJSONPath := filepath.Join(runtimeRoot, testNamespace, id, "state.json")
	if _, err = os.Stat(stateJSONPath); err != nil {
		t.Errorf("error while getting stat for %s: %v", stateJSONPath, err)
	}

	finishedC, err := task.Wait(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = task.Kill(ctx, syscall.SIGKILL); err != nil {
		t.Error(err)
	}
	<-finishedC
}

// TestDaemonRuntimeRoot ensures plugin.linux.runtime_root is not ignored
func TestDaemonRuntimeRoot(t *testing.T) {
	testDaemonRuntimeRoot(t, false)
}

// TestDaemonRuntimeRootNoShim ensures plugin.linux.runtime_root is not ignored when no_shim is true
func TestDaemonRuntimeRootNoShim(t *testing.T) {
	t.Skip("no_shim is not functional now: https://github.com/containerd/containerd/issues/2181")
	testDaemonRuntimeRoot(t, true)
}
