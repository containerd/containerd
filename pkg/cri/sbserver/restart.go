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

package sbserver

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/containerd/containerd"
	containerdio "github.com/containerd/containerd/cio"
	"github.com/containerd/containerd/errdefs"
	containerdimages "github.com/containerd/containerd/images"
	"github.com/containerd/containerd/log"
	criconfig "github.com/containerd/containerd/pkg/cri/config"
	"github.com/containerd/containerd/pkg/cri/sbserver/podsandbox"
	"github.com/containerd/containerd/platforms"
	"github.com/containerd/typeurl/v2"
	"golang.org/x/sync/errgroup"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	cio "github.com/containerd/containerd/pkg/cri/io"
	containerstore "github.com/containerd/containerd/pkg/cri/store/container"
	sandboxstore "github.com/containerd/containerd/pkg/cri/store/sandbox"
	ctrdutil "github.com/containerd/containerd/pkg/cri/util"
	"github.com/containerd/containerd/pkg/netns"
)

// NOTE: The recovery logic has following assumption: when the cri plugin is down:
// 1) Files (e.g. root directory, netns) and checkpoint maintained by the plugin MUST NOT be
// touched. Or else, recovery logic for those containers/sandboxes may return error.
// 2) Containerd containers may be deleted, but SHOULD NOT be added. Or else, recovery logic
// for the newly added container/sandbox will return error, because there is no corresponding root
// directory created.
// 3) Containerd container tasks may exit or be stopped, deleted. Even though current logic could
// tolerant tasks being created or started, we prefer that not to happen.

// recover recovers system state from containerd and status checkpoint.
func (c *criService) recover(ctx context.Context) error {
	// Recover all sandboxes.
	sandboxes, err := c.client.Containers(ctx, filterLabel(containerKindLabel, containerKindSandbox))
	if err != nil {
		return fmt.Errorf("failed to list sandbox containers: %w", err)
	}

	eg, ctx2 := errgroup.WithContext(ctx)
	for _, sandbox := range sandboxes {
		sandbox := sandbox
		eg.Go(func() error {
			sb, err := c.loadSandbox(ctx2, sandbox)
			if err != nil {
				log.G(ctx2).WithError(err).Errorf("Failed to load sandbox %q", sandbox.ID())
				return nil
			}
			log.G(ctx2).Debugf("Loaded sandbox %+v", sb)
			if err := c.sandboxStore.Add(sb); err != nil {
				return fmt.Errorf("failed to add sandbox %q to store: %w", sandbox.ID(), err)
			}
			if err := c.sandboxNameIndex.Reserve(sb.Name, sb.ID); err != nil {
				return fmt.Errorf("failed to reserve sandbox name %q: %w", sb.Name, err)
			}
			return nil
		})
	}
	if err := eg.Wait(); err != nil {
		return err
	}

	// Recover sandboxes in the new SandboxStore
	storedSandboxes, err := c.client.SandboxStore().List(ctx)
	if err != nil {
		return fmt.Errorf("failed to list sandboxes from API: %w", err)
	}
	for _, sbx := range storedSandboxes {
		if _, err := c.sandboxStore.Get(sbx.ID); err == nil {
			continue
		}

		metadata := sandboxstore.Metadata{}
		err := sbx.GetExtension(podsandbox.MetadataKey, &metadata)
		if err != nil {
			return fmt.Errorf("failed to get metadata for stored sandbox %q: %w", sbx.ID, err)
		}

		var (
			state      = sandboxstore.StateUnknown
			controller = c.sandboxControllers[criconfig.ModeShim]
		)

		status, err := controller.Status(ctx, sbx.ID, false)
		if err != nil {
			log.G(ctx).WithError(err).Error("failed to recover sandbox state")
			if errdefs.IsNotFound(err) {
				state = sandboxstore.StateNotReady
			}
		} else {
			if code, ok := runtime.PodSandboxState_value[status.State]; ok {
				if code == int32(runtime.PodSandboxState_SANDBOX_READY) {
					state = sandboxstore.StateReady
				} else if code == int32(runtime.PodSandboxState_SANDBOX_NOTREADY) {
					state = sandboxstore.StateNotReady
				}
			}
		}

		sb := sandboxstore.NewSandbox(metadata, sandboxstore.Status{State: state})

		// Load network namespace.
		sb.NetNS = getNetNS(&metadata)

		if err := c.sandboxStore.Add(sb); err != nil {
			return fmt.Errorf("failed to add stored sandbox %q to store: %w", sbx.ID, err)
		}
	}

	// Recover all containers.
	containers, err := c.client.Containers(ctx, filterLabel(containerKindLabel, containerKindContainer))
	if err != nil {
		return fmt.Errorf("failed to list containers: %w", err)
	}
	eg, ctx2 = errgroup.WithContext(ctx)
	for _, container := range containers {
		container := container
		eg.Go(func() error {
			cntr, err := c.loadContainer(ctx2, container)
			if err != nil {
				log.G(ctx2).WithError(err).Errorf("Failed to load container %q", container.ID())
				return nil
			}
			log.G(ctx2).Debugf("Loaded container %+v", cntr)
			if err := c.containerStore.Add(cntr); err != nil {
				return fmt.Errorf("failed to add container %q to store: %w", container.ID(), err)
			}
			if err := c.containerNameIndex.Reserve(cntr.Name, cntr.ID); err != nil {
				return fmt.Errorf("failed to reserve container name %q: %w", cntr.Name, err)
			}
			return nil
		})
	}
	if err := eg.Wait(); err != nil {
		return err
	}

	// Recover all images.
	cImages, err := c.client.ListImages(ctx)
	if err != nil {
		return fmt.Errorf("failed to list images: %w", err)
	}
	c.loadImages(ctx, cImages)

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
		if err := cleanupOrphanedIDDirs(ctx, cleanup.cntrs, cleanup.base); err != nil {
			return fmt.Errorf("%s: %w", cleanup.errMsg, err)
		}
	}
	return nil
}

// loadContainerTimeout is the default timeout for loading a container/sandbox.
// One container/sandbox hangs (e.g. containerd#2438) should not affect other
// containers/sandboxes.
// Most CRI container/sandbox related operations are per container, the ones
// which handle multiple containers at a time are:
// * ListPodSandboxes: Don't talk with containerd services.
// * ListContainers: Don't talk with containerd services.
// * ListContainerStats: Not in critical code path, a default timeout will
// be applied at CRI level.
// * Recovery logic: We should set a time for each container/sandbox recovery.
// * Event monitor: We should set a timeout for each container/sandbox event handling.
const loadContainerTimeout = 10 * time.Second

// loadContainer loads container from containerd and status checkpoint.
func (c *criService) loadContainer(ctx context.Context, cntr containerd.Container) (containerstore.Container, error) {
	ctx, cancel := context.WithTimeout(ctx, loadContainerTimeout)
	defer cancel()
	id := cntr.ID()
	containerDir := c.getContainerRootDir(id)
	volatileContainerDir := c.getVolatileContainerRootDir(id)
	var container containerstore.Container
	// Load container metadata.
	exts, err := cntr.Extensions(ctx)
	if err != nil {
		return container, fmt.Errorf("failed to get container extensions: %w", err)
	}
	ext, ok := exts[containerMetadataExtension]
	if !ok {
		return container, fmt.Errorf("metadata extension %q not found", containerMetadataExtension)
	}
	data, err := typeurl.UnmarshalAny(ext)
	if err != nil {
		return container, fmt.Errorf("failed to unmarshal metadata extension %q: %w", ext, err)
	}
	meta := data.(*containerstore.Metadata)

	// Load status from checkpoint.
	status, err := containerstore.LoadStatus(containerDir, id)
	if err != nil {
		log.G(ctx).WithError(err).Warnf("Failed to load container status for %q", id)
		status = unknownContainerStatus()
	}

	var containerIO *cio.ContainerIO
	err = func() error {
		// Load up-to-date status from containerd.
		t, err := cntr.Task(ctx, func(fifos *containerdio.FIFOSet) (_ containerdio.IO, err error) {
			stdoutWC, stderrWC, err := c.createContainerLoggers(meta.LogPath, meta.Config.GetTty())
			if err != nil {
				return nil, err
			}
			defer func() {
				if err != nil {
					if stdoutWC != nil {
						stdoutWC.Close()
					}
					if stderrWC != nil {
						stderrWC.Close()
					}
				}
			}()
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
			return fmt.Errorf("failed to load task: %w", err)
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
					return fmt.Errorf("failed to get task status: %w", err)
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
					return fmt.Errorf("failed to create container io: %w", err)
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
					return fmt.Errorf("failed to delete task: %w", err)
				}
				if status.State() != runtime.ContainerState_CONTAINER_CREATED {
					return fmt.Errorf("unexpected container state for created task: %q", status.State())
				}
			case containerd.Running:
				// Task is running. Container must be in `RUNNING` state, based on our assumption that
				// "task should not be started when containerd is down".
				switch status.State() {
				case runtime.ContainerState_CONTAINER_EXITED:
					return fmt.Errorf("unexpected container state for running task: %q", status.State())
				case runtime.ContainerState_CONTAINER_RUNNING:
				default:
					// This may happen if containerd gets restarted after task is started, but
					// before status is checkpointed.
					status.StartedAt = time.Now().UnixNano()
					status.Pid = t.Pid()
				}
				// Wait for the task for exit monitor.
				// wait is a long running background request, no timeout needed.
				exitCh, err := t.Wait(ctrdutil.NamespacedContext())
				if err != nil {
					if !errdefs.IsNotFound(err) {
						return fmt.Errorf("failed to wait for task: %w", err)
					}
					// Container was in running state, but its task has been deleted,
					// set unknown exited state.
					status.FinishedAt = time.Now().UnixNano()
					status.ExitCode = unknownExitCode
					status.Reason = unknownExitReason
				} else {
					// Start exit monitor.
					c.eventMonitor.startContainerExitMonitor(context.Background(), id, status.Pid, exitCh)
				}
			case containerd.Stopped:
				// Task is stopped. Update status and delete the task.
				if _, err := t.Delete(ctx, containerd.WithProcessKill); err != nil && !errdefs.IsNotFound(err) {
					return fmt.Errorf("failed to delete task: %w", err)
				}
				status.FinishedAt = s.ExitTime.UnixNano()
				status.ExitCode = int32(s.ExitStatus)
			default:
				return fmt.Errorf("unexpected task status %q", s.Status)
			}
		}
		return nil
	}()
	if err != nil {
		log.G(ctx).WithError(err).Errorf("Failed to load container status for %q", id)
		// Only set the unknown field in this case, because other fields may
		// contain useful information loaded from the checkpoint.
		status.Unknown = true
	}
	opts := []containerstore.Opts{
		containerstore.WithStatus(status, containerDir),
		containerstore.WithContainer(cntr),
	}
	// containerIO could be nil for container in unknown state.
	if containerIO != nil {
		opts = append(opts, containerstore.WithContainerIO(containerIO))
	}
	return containerstore.NewContainer(*meta, opts...)
}

// loadSandbox loads sandbox from containerd.
func (c *criService) loadSandbox(ctx context.Context, cntr containerd.Container) (sandboxstore.Sandbox, error) {
	ctx, cancel := context.WithTimeout(ctx, loadContainerTimeout)
	defer cancel()
	var sandbox sandboxstore.Sandbox
	// Load sandbox metadata.
	exts, err := cntr.Extensions(ctx)
	if err != nil {
		return sandbox, fmt.Errorf("failed to get sandbox container extensions: %w", err)
	}
	ext, ok := exts[sandboxMetadataExtension]
	if !ok {
		return sandbox, fmt.Errorf("metadata extension %q not found", sandboxMetadataExtension)
	}
	data, err := typeurl.UnmarshalAny(ext)
	if err != nil {
		return sandbox, fmt.Errorf("failed to unmarshal metadata extension %q: %w", ext, err)
	}
	meta := data.(*sandboxstore.Metadata)

	s, err := func() (sandboxstore.Status, error) {
		status := unknownSandboxStatus()
		// Load sandbox created timestamp.
		info, err := cntr.Info(ctx)
		if err != nil {
			return status, fmt.Errorf("failed to get sandbox container info: %w", err)
		}
		status.CreatedAt = info.CreatedAt

		// Load sandbox state.
		t, err := cntr.Task(ctx, nil)
		if err != nil && !errdefs.IsNotFound(err) {
			return status, fmt.Errorf("failed to load task: %w", err)
		}
		var taskStatus containerd.Status
		var notFound bool
		if errdefs.IsNotFound(err) {
			// Task is not found.
			notFound = true
		} else {
			// Task is found. Get task status.
			taskStatus, err = t.Status(ctx)
			if err != nil {
				// It's still possible that task is deleted during this window.
				if !errdefs.IsNotFound(err) {
					return status, fmt.Errorf("failed to get task status: %w", err)
				}
				notFound = true
			}
		}
		if notFound {
			// Task does not exist, set sandbox state as NOTREADY.
			status.State = sandboxstore.StateNotReady
		} else {
			if taskStatus.Status == containerd.Running {
				// Wait for the task for sandbox monitor.
				// wait is a long running background request, no timeout needed.
				exitCh, err := t.Wait(ctrdutil.NamespacedContext())
				if err != nil {
					if !errdefs.IsNotFound(err) {
						return status, fmt.Errorf("failed to wait for task: %w", err)
					}
					status.State = sandboxstore.StateNotReady
				} else {
					// Task is running, set sandbox state as READY.
					status.State = sandboxstore.StateReady
					status.Pid = t.Pid()
					c.eventMonitor.startSandboxExitMonitor(context.Background(), meta.ID, status.Pid, exitCh)
				}
			} else {
				// Task is not running. Delete the task and set sandbox state as NOTREADY.
				if _, err := t.Delete(ctx, containerd.WithProcessKill); err != nil && !errdefs.IsNotFound(err) {
					return status, fmt.Errorf("failed to delete task: %w", err)
				}
				status.State = sandboxstore.StateNotReady
			}
		}
		return status, nil
	}()
	if err != nil {
		log.G(ctx).WithError(err).Errorf("Failed to load sandbox status for %q", cntr.ID())
	}

	sandbox = sandboxstore.NewSandbox(*meta, s)
	sandbox.Container = cntr

	// Load network namespace.
	sandbox.NetNS = getNetNS(meta)

	// It doesn't matter whether task is running or not. If it is running, sandbox
	// status will be `READY`; if it is not running, sandbox status will be `NOT_READY`,
	// kubelet will stop the sandbox which will properly cleanup everything.
	return sandbox, nil
}

func getNetNS(meta *sandboxstore.Metadata) *netns.NetNS {
	// Don't need to load netns for host network sandbox.
	if hostNetwork(meta.Config) {
		return nil
	}
	return netns.LoadNetNS(meta.NetNSPath)
}

// loadImages loads images from containerd.
func (c *criService) loadImages(ctx context.Context, cImages []containerd.Image) {
	snapshotter := c.config.ContainerdConfig.Snapshotter
	var wg sync.WaitGroup
	for _, i := range cImages {
		wg.Add(1)
		i := i
		go func() {
			defer wg.Done()
			ok, _, _, _, err := containerdimages.Check(ctx, i.ContentStore(), i.Target(), platforms.Default())
			if err != nil {
				log.G(ctx).WithError(err).Errorf("Failed to check image content readiness for %q", i.Name())
				return
			}
			if !ok {
				log.G(ctx).Warnf("The image content readiness for %q is not ok", i.Name())
				return
			}
			// Checking existence of top-level snapshot for each image being recovered.
			unpacked, err := i.IsUnpacked(ctx, snapshotter)
			if err != nil {
				log.G(ctx).WithError(err).Warnf("Failed to check whether image is unpacked for image %s", i.Name())
				return
			}
			if !unpacked {
				log.G(ctx).Warnf("The image %s is not unpacked.", i.Name())
				// TODO(random-liu): Consider whether we should try unpack here.
			}
			if err := c.updateImage(ctx, i.Name()); err != nil {
				log.G(ctx).WithError(err).Warnf("Failed to update reference for image %q", i.Name())
				return
			}
			log.G(ctx).Debugf("Loaded image %q", i.Name())
		}()
	}
	wg.Wait()
}

func cleanupOrphanedIDDirs(ctx context.Context, cntrs []containerd.Container, base string) error {
	// Cleanup orphaned id directories.
	dirs, err := os.ReadDir(base)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to read base directory: %w", err)
	}
	idsMap := make(map[string]containerd.Container)
	for _, cntr := range cntrs {
		idsMap[cntr.ID()] = cntr
	}
	for _, d := range dirs {
		if !d.IsDir() {
			log.G(ctx).Warnf("Invalid file %q found in base directory %q", d.Name(), base)
			continue
		}
		if _, ok := idsMap[d.Name()]; ok {
			// Do not remove id directory if corresponding container is found.
			continue
		}
		dir := filepath.Join(base, d.Name())
		if err := ensureRemoveAll(ctx, dir); err != nil {
			log.G(ctx).WithError(err).Warnf("Failed to remove id directory %q", dir)
		} else {
			log.G(ctx).Debugf("Cleanup orphaned id directory %q", dir)
		}
	}
	return nil
}
