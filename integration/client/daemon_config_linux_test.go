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

package client

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/containerd/cgroups/v3"
	. "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/oci"
	"github.com/containerd/containerd/v2/plugins"
	"github.com/containerd/containerd/v2/runtime/v2/runc/options"
)

// TestDaemonRuntimeRoot ensures plugin.linux.runtime_root is not ignored
func TestDaemonRuntimeRoot(t *testing.T) {
	runtimeRoot := t.TempDir()
	configTOML := `
version = 2
[plugins]
 [plugins."io.containerd.grpc.v1.cri"]
   stream_server_port = "0"
`

	client, _, cleanup := newDaemonWithConfig(t, configTOML)
	defer cleanup()

	ctx, cancel := testContext(t)
	defer cancel()
	// FIXME(AkihiroSuda): import locally frozen image?
	image, err := client.Pull(ctx, testImage, WithPullUnpack)
	if err != nil {
		t.Fatal(err)
	}

	id := t.Name()
	container, err := client.NewContainer(ctx, id, WithNewSnapshot(id, image), WithNewSpec(oci.WithImageConfig(image), withProcessArgs("top")), WithRuntime(plugins.RuntimeRuncV2, &options.Options{
		Root: runtimeRoot,
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer container.Delete(ctx, WithSnapshotCleanup)

	task, err := container.NewTask(ctx, empty())
	if err != nil {
		t.Fatal(err)
	}
	defer task.Delete(ctx)

	status, err := task.Wait(ctx)
	if err != nil {
		t.Fatal(err)
	}

	containerPath := filepath.Join(runtimeRoot, testNamespace, id)
	if _, err = os.Stat(containerPath); err != nil {
		t.Errorf("error while getting stat for %s: %v", containerPath, err)
	}

	if err = task.Kill(ctx, syscall.SIGKILL); err != nil {
		t.Error(err)
	}
	<-status
}

// code most copy from https://github.com/opencontainers/runc
func getCgroupPath() (map[string]string, error) {
	cgroupPath := make(map[string]string)
	f, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return nil, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		text := scanner.Text()
		fields := strings.Split(text, " ")
		// Safe as mountinfo encodes mountpoints with spaces as \040.
		index := strings.Index(text, " - ")
		postSeparatorFields := strings.Fields(text[index+3:])
		numPostFields := len(postSeparatorFields)

		// This is an error as we can't detect if the mount is for "cgroup"
		if numPostFields == 0 {
			continue
		}

		if postSeparatorFields[0] == "cgroup" {
			// Check that the mount is properly formatted.
			if numPostFields < 3 {
				continue
			}
			cgroupPath[filepath.Base(fields[4])] = fields[4]
		}
	}

	return cgroupPath, nil
}

// TestDaemonCustomCgroup ensures plugin.cgroup.path is not ignored
func TestDaemonCustomCgroup(t *testing.T) {
	if cgroups.Mode() == cgroups.Unified {
		t.Skip("test requires cgroup1")
	}
	cgroupPath, err := getCgroupPath()
	if err != nil {
		t.Fatal(err)
	}
	if len(cgroupPath) == 0 {
		t.Skip("skip TestDaemonCustomCgroup since no cgroup path available")
	}

	customCgroup := strconv.Itoa(time.Now().Nanosecond())
	configTOML := `
version = 2
[cgroup]
  path = "` + customCgroup + `"`

	_, _, cleanup := newDaemonWithConfig(t, configTOML)

	defer func() {
		// do cgroup path clean
		for _, v := range cgroupPath {
			if _, err := os.Stat(filepath.Join(v, customCgroup)); err == nil {
				if err := os.RemoveAll(filepath.Join(v, customCgroup)); err != nil {
					t.Logf("failed to remove cgroup path %s", filepath.Join(v, customCgroup))
				}
			}
		}
	}()

	defer cleanup()

	paths := []string{
		"devices",
		"memory",
		"cpu",
		"blkio",
	}

	for _, p := range paths {
		v := cgroupPath[p]
		if v == "" {
			continue
		}
		path := filepath.Join(v, customCgroup)
		if _, err := os.Stat(path); err != nil {
			if os.IsNotExist(err) {
				t.Fatalf("custom cgroup path %s should exist, actually not", path)
			}
		}
	}
}
