/*
Copyright 2017 The Kubernetes Authors.

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

package server

import (
	gocontext "context"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/containers"
	"github.com/golang/glog"
	"golang.org/x/net/context"
	"golang.org/x/sys/unix"

	"github.com/kubernetes-incubator/cri-containerd/pkg/util"
)

// TODO(random-liu): Remove this after debugging.
// checkSnapshot checks whether rootfs is empty, or whether entrypoint exists. If not,
// it dumps the root fs.
func (c *criContainerdService) checkSnapshot(ctx context.Context, snapshot, entrypoint string, env []string, mustDump bool) error {
	glog.V(0).Infof("Check snapshot %q with entrypoint %q", snapshot, entrypoint)
	snapshotter := c.client.SnapshotService(c.config.ContainerdConfig.Snapshotter)
	mounts, err := snapshotter.Mounts(ctx, snapshot)
	if err != nil {
		return err
	}
	root, err := ioutil.TempDir("", "ctd-rootfs")
	if err != nil {
		return err
	}
	defer os.RemoveAll(root) // nolint: errcheck
	for _, m := range mounts {
		if err := m.Mount(root); err != nil {
			return err
		}
	}
	defer unix.Unmount(root, 0) // nolint: errcheck
	fs, err := ioutil.ReadDir(root)
	if err != nil {
		return err
	}
	if len(fs) == 0 {
		dumpDir(root) // nolint: errcheck
		return fmt.Errorf("rootfs is empty")
	}
	if entrypoint != "" {
		paths := append(getPath(env), "")
		found := false
		for _, p := range paths {
			e := filepath.Join(root, p, entrypoint)
			_, err = os.Stat(e)
			if err == nil {
				found = true
				continue
			}
		}
		if !found {
			dumpDir(root) // nolint: errcheck
			return fmt.Errorf("entrypoint %q not found", entrypoint)
		}
	}
	if mustDump {
		dumpDir(root) // nolint: errcheck
	}
	return nil
}

// checkImage create a snapshot from the image and check rootfs.
func (c *criContainerdService) checkImage(ctx context.Context, imageID, entrypoint string, env []string, mustDump bool) error {
	glog.V(0).Infof("Check image %q with entrypoint %q", imageID, entrypoint)
	snapshotter := c.client.SnapshotService(c.config.ContainerdConfig.Snapshotter)
	ctx, done, err := c.client.WithLease(ctx)
	if err != nil {
		return err
	}
	defer done() // nolint: errcheck
	img, err := c.imageStore.Get(imageID)
	if err != nil {
		return err
	}
	sid := util.GenerateID()
	_, err = snapshotter.Prepare(ctx, sid, img.ChainID)
	if err != nil {
		return err
	}
	defer func() {
		if err := snapshotter.Remove(ctx, sid); err != nil {
			glog.Errorf("Failed to remove snapshot id %q: %v", sid, err)
		}
	}()
	if entrypoint == "" {
		if len(img.Config.Entrypoint) > 0 {
			entrypoint = img.Config.Entrypoint[0]
		}
	}
	if len(env) == 0 {
		if len(img.Config.Env) > 0 {
			env = img.Config.Env
		}
	}
	return c.checkSnapshot(ctx, sid, entrypoint, env, mustDump)
}

func withSnapshotCheck(c *criContainerdService, id, entrypoint string, env []string) containerd.NewContainerOpts {
	return func(ctx gocontext.Context, client *containerd.Client, _ *containers.Container) error {
		glog.V(0).Infof("Check snapshot %q after snapshot creation", id)
		if err := c.checkSnapshot(ctx, id, entrypoint, env, false); err != nil {
			glog.Errorf("Failed to check snapshot %q: %v", id, err)
		}
		return nil
	}
}

func getPath(env []string) []string {
	for _, e := range env {
		kv := strings.SplitN(e, "=", 2)
		if len(kv) != 2 {
			continue
		}
		k, v := kv[0], kv[1]
		if k != "PATH" {
			continue
		}
		return strings.Split(v, ":")
	}
	return nil
}

func dumpDir(root string) error {
	return filepath.Walk(root, func(path string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if fi.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			glog.V(0).Info(fi.Mode(), fmt.Sprintf("%10s", ""), path, "->", target)
		} else if fi.Mode().IsRegular() {
			p, err := ioutil.ReadFile(path)
			if err != nil {
				glog.Errorf("Error reading file %q: %v", path, err)
				return nil
			}

			if len(p) > 64 { // just display a little bit.
				p = p[:64]
			}
			glog.V(0).Info(fi.Mode(), fmt.Sprintf("%10d", fi.Size()), path, "[", strconv.Quote(string(p)), "...]")
		} else {
			glog.V(0).Info(fi.Mode(), fmt.Sprintf("%10d", fi.Size()), path)
		}
		return nil
	})
}
