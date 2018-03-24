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
	"io/ioutil"
	"os"
	"path/filepath"
	"time"

	"github.com/containerd/containerd"
	containerdio "github.com/containerd/containerd/cio"
	"github.com/containerd/containerd/errdefs"
	containerdimages "github.com/containerd/containerd/images"
	"github.com/containerd/containerd/platforms"
	"github.com/containerd/typeurl"
	"github.com/docker/distribution/reference"
	"github.com/docker/docker/pkg/system"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"golang.org/x/net/context"
	runtime "k8s.io/kubernetes/pkg/kubelet/apis/cri/runtime/v1alpha2"

	cio "github.com/containerd/cri/pkg/server/io"
	containerstore "github.com/containerd/cri/pkg/store/container"
	imagestore "github.com/containerd/cri/pkg/store/image"
	sandboxstore "github.com/containerd/cri/pkg/store/sandbox"
)

// NOTE: The recovery logic has following assumption: when the cri plugin is down:
// 1) Files (e.g. root directory, netns) and checkpoint maintained by the plugin MUST NOT be
// touched. Or else, recovery logic for those containers/sandboxes may return error.
// 2) Containerd containers may be deleted, but SHOULD NOT be added. Or else, recovery logic
// for the newly added container/sandbox will return error, because there is no corresponding root
// directory created.
// 3) Containerd container tasks may exit or be stoppped, deleted. Even though current logic could
// tolerant tasks being created or started, we prefer that not to happen.

// recover recovers system state from containerd and status checkpoint.
func (c *criService) recover(ctx context.Context) error {
	// Recover all sandboxes.
	sandboxes, err := c.client.Containers(ctx, filterLabel(containerKindLabel, containerKindSandbox))
	if err != nil {
		return errors.Wrap(err, "failed to list sandbox containers")
	}
	for _, sandbox := range sandboxes {
		sb, err := loadSandbox(ctx, sandbox)
		if err != nil {
			logrus.WithError(err).Errorf("Failed to load sandbox %q", sandbox.ID())
			continue
		}
		logrus.Debugf("Loaded sandbox %+v", sb)
		if err := c.sandboxStore.Add(sb); err != nil {
			return errors.Wrapf(err, "failed to add sandbox %q to store", sandbox.ID())
		}
		if err := c.sandboxNameIndex.Reserve(sb.Name, sb.ID); err != nil {
			return errors.Wrapf(err, "failed to reserve sandbox name %q", sb.Name)
		}
	}

	// Recover all containers.
	containers, err := c.client.Containers(ctx, filterLabel(containerKindLabel, containerKindContainer))
	if err != nil {
		return errors.Wrap(err, "failed to list containers")
	}
	for _, container := range containers {
		containerDir := c.getContainerRootDir(container.ID())
		volatileContainerDir := c.getVolatileContainerRootDir(container.ID())
		cntr, err := loadContainer(ctx, container, containerDir, volatileContainerDir)
		if err != nil {
			logrus.WithError(err).Errorf("Failed to load container %q", container.ID())
			continue
		}
		logrus.Debugf("Loaded container %+v", cntr)
		if err := c.containerStore.Add(cntr); err != nil {
			return errors.Wrapf(err, "failed to add container %q to store", container.ID())
		}
		if err := c.containerNameIndex.Reserve(cntr.Name, cntr.ID); err != nil {
			return errors.Wrapf(err, "failed to reserve container name %q", cntr.Name)
		}
	}

	// Recover all images.
	cImages, err := c.client.ListImages(ctx)
	if err != nil {
		return errors.Wrap(err, "failed to list images")
	}
	images, err := loadImages(ctx, cImages, c.config.ContainerdConfig.Snapshotter)
	if err != nil {
		return errors.Wrap(err, "failed to load images")
	}
	for _, image := range images {
		logrus.Debugf("Loaded image %+v", image)
		if err := c.imageStore.Add(image); err != nil {
			return errors.Wrapf(err, "failed to add image %q to store", image.ID)
		}
	}

	// It's possible that containerd containers are deleted unexpectedly. In that case,
	// we can't even get metadata, we should cleanup orphaned sandbox/container directories
	// with best effort.

	// Cleanup orphaned sandbox and container directories without corresponding containerd container.
	for _, cleanup := range []struct {
		cntrs  []containerd.Container
		base   string
		errMsg string
	}{
		{
			cntrs:  sandboxes,
			base:   filepath.Join(c.config.RootDir, sandboxesDir),
			errMsg: "failed to cleanup orphaned sandbox directories",
		},
		{
			cntrs:  sandboxes,
			base:   filepath.Join(c.config.StateDir, sandboxesDir),
			errMsg: "failed to cleanup orphaned volatile sandbox directories",
		},
		{
			cntrs:  containers,
			base:   filepath.Join(c.config.RootDir, containersDir),
			errMsg: "failed to cleanup orphaned container directories",
		},
		{
			cntrs:  containers,
			base:   filepath.Join(c.config.StateDir, containersDir),
			errMsg: "failed to cleanup orphaned volatile container directories",
		},
	} {
		if err := cleanupOrphanedIDDirs(cleanup.cntrs, cleanup.base); err != nil {
			return errors.Wrap(err, cleanup.errMsg)
		}
	}
	return nil
}

// loadContainer loads container from containerd and status checkpoint.
func loadContainer(ctx context.Context, cntr containerd.Container, containerDir, volatileContainerDir string) (containerstore.Container, error) {
	id := cntr.ID()
	var container containerstore.Container
	// Load container metadata.
	exts, err := cntr.Extensions(ctx)
	if err != nil {
		return container, errors.Wrap(err, "failed to get container extensions")
	}
	ext, ok := exts[containerMetadataExtension]
	if !ok {
		return container, errors.Errorf("metadata extension %q not found", containerMetadataExtension)
	}
	data, err := typeurl.UnmarshalAny(&ext)
	if err != nil {
		return container, errors.Wrapf(err, "failed to unmarshal metadata extension %q", ext)
	}
	meta := data.(*containerstore.Metadata)

	// Load status from checkpoint.
	status, err := containerstore.LoadStatus(containerDir, id)
	if err != nil {
		logrus.WithError(err).Warnf("Failed to load container status for %q", id)
		status = unknownContainerStatus()
	}

	// Load up-to-date status from containerd.
	var containerIO *cio.ContainerIO
	t, err := cntr.Task(ctx, func(fifos *containerdio.FIFOSet) (containerdio.IO, error) {
		stdoutWC, stderrWC, err := createContainerLoggers(meta.LogPath, meta.Config.GetTty())
		if err != nil {
			return nil, err
		}
		containerIO, err = cio.NewContainerIO(id,
			cio.WithFIFOs(fifos),
		)
		if err != nil {
			return nil, err
		}
		containerIO.AddOutput("log", stdoutWC, stderrWC)
		containerIO.Pipe()
		return containerIO, nil
	})
	if err != nil && !errdefs.IsNotFound(err) {
		return container, errors.Wrap(err, "failed to load task")
	}
	var s containerd.Status
	var notFound bool
	if errdefs.IsNotFound(err) {
		// Task is not found.
		notFound = true
	} else {
		// Task is found. Get task status.
		s, err = t.Status(ctx)
		if err != nil {
			// It's still possible that task is deleted during this window.
			if !errdefs.IsNotFound(err) {
				return container, errors.Wrap(err, "failed to get task status")
			}
			notFound = true
		}
	}
	if notFound {
		// Task is not created or has been deleted, use the checkpointed status
		// to generate container status.
		switch status.State() {
		case runtime.ContainerState_CONTAINER_CREATED:
			// NOTE: Another possibility is that we've tried to start the container, but
			// containerd got restarted during that. In that case, we still
			// treat the container as `CREATED`.
			containerIO, err = cio.NewContainerIO(id,
				cio.WithNewFIFOs(volatileContainerDir, meta.Config.GetTty(), meta.Config.GetStdin()),
			)
			if err != nil {
				return container, errors.Wrap(err, "failed to create container io")
			}
		case runtime.ContainerState_CONTAINER_RUNNING:
			// Container was in running state, but its task has been deleted,
			// set unknown exited state. Container io is not needed in this case.
			status.FinishedAt = time.Now().UnixNano()
			status.ExitCode = unknownExitCode
			status.Reason = unknownExitReason
		default:
			// Container is in exited/unknown state, return the status as it is.
		}
	} else {
		// Task status is found. Update container status based on the up-to-date task status.
		switch s.Status {
		case containerd.Created:
			// Task has been created, but not started yet. This could only happen if containerd
			// gets restarted during container start.
			// Container must be in `CREATED` state.
			if _, err := t.Delete(ctx, containerd.WithProcessKill); err != nil && !errdefs.IsNotFound(err) {
				return container, errors.Wrap(err, "failed to delete task")
			}
			if status.State() != runtime.ContainerState_CONTAINER_CREATED {
				return container, errors.Errorf("unexpected container state for created task: %q", status.State())
			}
		case containerd.Running:
			// Task is running. Container must be in `RUNNING` state, based on our assuption that
			// "task should not be started when containerd is down".
			switch status.State() {
			case runtime.ContainerState_CONTAINER_EXITED:
				return container, errors.Errorf("unexpected container state for running task: %q", status.State())
			case runtime.ContainerState_CONTAINER_RUNNING:
			default:
				// This may happen if containerd gets restarted after task is started, but
				// before status is checkpointed.
				status.StartedAt = time.Now().UnixNano()
				status.Pid = t.Pid()
			}
		case containerd.Stopped:
			// Task is stopped. Updata status and delete the task.
			if _, err := t.Delete(ctx, containerd.WithProcessKill); err != nil && !errdefs.IsNotFound(err) {
				return container, errors.Wrap(err, "failed to delete task")
			}
			status.FinishedAt = s.ExitTime.UnixNano()
			status.ExitCode = int32(s.ExitStatus)
		default:
			return container, errors.Errorf("unexpected task status %q", s.Status)
		}
	}
	opts := []containerstore.Opts{
		containerstore.WithStatus(status, containerDir),
		containerstore.WithContainer(cntr),
	}
	if containerIO != nil {
		opts = append(opts, containerstore.WithContainerIO(containerIO))
	}
	return containerstore.NewContainer(*meta, opts...)
}

const (
	// unknownExitCode is the exit code when exit reason is unknown.
	unknownExitCode = 255
	// unknownExitReason is the exit reason when exit reason is unknown.
	unknownExitReason = "Unknown"
)

// unknownContainerStatus returns the default container status when its status is unknown.
func unknownContainerStatus() containerstore.Status {
	return containerstore.Status{
		CreatedAt:  time.Now().UnixNano(),
		StartedAt:  time.Now().UnixNano(),
		FinishedAt: time.Now().UnixNano(),
		ExitCode:   unknownExitCode,
		Reason:     unknownExitReason,
	}
}

// loadSandbox loads sandbox from containerd.
func loadSandbox(ctx context.Context, cntr containerd.Container) (sandboxstore.Sandbox, error) {
	var sandbox sandboxstore.Sandbox
	// Load sandbox metadata.
	exts, err := cntr.Extensions(ctx)
	if err != nil {
		return sandbox, errors.Wrap(err, "failed to get sandbox container extensions")
	}
	ext, ok := exts[sandboxMetadataExtension]
	if !ok {
		return sandbox, errors.Errorf("metadata extension %q not found", sandboxMetadataExtension)
	}
	data, err := typeurl.UnmarshalAny(&ext)
	if err != nil {
		return sandbox, errors.Wrapf(err, "failed to unmarshal metadata extension %q", ext)
	}
	meta := data.(*sandboxstore.Metadata)

	// Load sandbox created timestamp.
	info, err := cntr.Info(ctx)
	if err != nil {
		return sandbox, errors.Wrap(err, "failed to get sandbox container info")
	}
	createdAt := info.CreatedAt

	// Load sandbox status.
	t, err := cntr.Task(ctx, nil)
	if err != nil && !errdefs.IsNotFound(err) {
		return sandbox, errors.Wrap(err, "failed to load task")
	}
	var s containerd.Status
	var notFound bool
	if errdefs.IsNotFound(err) {
		// Task is not found.
		notFound = true
	} else {
		// Task is found. Get task status.
		s, err = t.Status(ctx)
		if err != nil {
			// It's still possible that task is deleted during this window.
			if !errdefs.IsNotFound(err) {
				return sandbox, errors.Wrap(err, "failed to get task status")
			}
			notFound = true
		}
	}
	var state sandboxstore.State
	var pid uint32
	if notFound {
		// Task does not exist, set sandbox state as NOTREADY.
		state = sandboxstore.StateNotReady
	} else {
		if s.Status == containerd.Running {
			// Task is running, set sandbox state as READY.
			state = sandboxstore.StateReady
			pid = t.Pid()
		} else {
			// Task is not running. Delete the task and set sandbox state as NOTREADY.
			if _, err := t.Delete(ctx, containerd.WithProcessKill); err != nil && !errdefs.IsNotFound(err) {
				return sandbox, errors.Wrap(err, "failed to delete task")
			}
			state = sandboxstore.StateNotReady
		}
	}

	sandbox = sandboxstore.NewSandbox(
		*meta,
		sandboxstore.Status{
			Pid:       pid,
			CreatedAt: createdAt,
			State:     state,
		},
	)
	sandbox.Container = cntr

	// Load network namespace.
	if meta.Config.GetLinux().GetSecurityContext().GetNamespaceOptions().GetNetwork() == runtime.NamespaceMode_NODE {
		// Don't need to load netns for host network sandbox.
		return sandbox, nil
	}
	netNS, err := sandboxstore.LoadNetNS(meta.NetNSPath)
	if err != nil {
		if err != sandboxstore.ErrClosedNetNS {
			return sandbox, errors.Wrapf(err, "failed to load netns %q", meta.NetNSPath)
		}
		netNS = nil
	}
	sandbox.NetNS = netNS

	// It doesn't matter whether task is running or not. If it is running, sandbox
	// status will be `READY`; if it is not running, sandbox status will be `NOT_READY`,
	// kubelet will stop the sandbox which will properly cleanup everything.
	return sandbox, nil
}

// loadImages loads images from containerd.
// TODO(random-liu): Check whether image is unpacked, because containerd put image reference
// into store before image is unpacked.
func loadImages(ctx context.Context, cImages []containerd.Image,
	snapshotter string) ([]imagestore.Image, error) {
	// Group images by image id.
	imageMap := make(map[string][]containerd.Image)
	for _, i := range cImages {
		desc, err := i.Config(ctx)
		if err != nil {
			logrus.WithError(err).Warnf("Failed to get image config for %q", i.Name())
			continue
		}
		id := desc.Digest.String()
		imageMap[id] = append(imageMap[id], i)
	}
	var images []imagestore.Image
	for id, imgs := range imageMap {
		// imgs len must be > 0, or else the entry will not be created in
		// previous loop.
		i := imgs[0]
		ok, _, _, _, err := containerdimages.Check(ctx, i.ContentStore(), i.Target(), platforms.Default())
		if err != nil {
			logrus.WithError(err).Errorf("Failed to check image content readiness for %q", i.Name())
			continue
		}
		if !ok {
			logrus.Warnf("The image content readiness for %q is not ok", i.Name())
			continue
		}
		// Checking existence of top-level snapshot for each image being recovered.
		unpacked, err := i.IsUnpacked(ctx, snapshotter)
		if err != nil {
			logrus.WithError(err).Warnf("Failed to Check whether image is unpacked for image %s", i.Name())
			continue
		}
		if !unpacked {
			logrus.Warnf("The image %s is not unpacked.", i.Name())
			// TODO(random-liu): Consider whether we should try unpack here.
		}

		info, err := getImageInfo(ctx, i)
		if err != nil {
			logrus.WithError(err).Warnf("Failed to get image info for %q", i.Name())
			continue
		}
		image := imagestore.Image{
			ID:        id,
			ChainID:   info.chainID.String(),
			Size:      info.size,
			ImageSpec: info.imagespec,
			Image:     i,
		}
		// Recover repo digests and repo tags.
		for _, i := range imgs {
			name := i.Name()
			r, err := reference.ParseAnyReference(name)
			if err != nil {
				logrus.WithError(err).Warnf("Failed to parse image reference %q", name)
				continue
			}
			if _, ok := r.(reference.Canonical); ok {
				image.RepoDigests = append(image.RepoDigests, name)
			} else if _, ok := r.(reference.Tagged); ok {
				image.RepoTags = append(image.RepoTags, name)
			} else if _, ok := r.(reference.Digested); ok {
				// This is an image id.
				continue
			} else {
				logrus.Warnf("Invalid image reference %q", name)
			}
		}
		images = append(images, image)
	}
	return images, nil
}

func cleanupOrphanedIDDirs(cntrs []containerd.Container, base string) error {
	// Cleanup orphaned id directories.
	dirs, err := ioutil.ReadDir(base)
	if err != nil && !os.IsNotExist(err) {
		return errors.Wrap(err, "failed to read base directory")
	}
	idsMap := make(map[string]containerd.Container)
	for _, cntr := range cntrs {
		idsMap[cntr.ID()] = cntr
	}
	for _, d := range dirs {
		if !d.IsDir() {
			logrus.Warnf("Invalid file %q found in base directory %q", d.Name(), base)
			continue
		}
		if _, ok := idsMap[d.Name()]; ok {
			// Do not remove id directory if corresponding container is found.
			continue
		}
		dir := filepath.Join(base, d.Name())
		if err := system.EnsureRemoveAll(dir); err != nil {
			logrus.WithError(err).Warnf("Failed to remove id directory %q", dir)
		} else {
			logrus.Debugf("Cleanup orphaned id directory %q", dir)
		}
	}
	return nil
}
