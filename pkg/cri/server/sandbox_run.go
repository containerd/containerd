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

package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"time"

	cni "github.com/containerd/go-cni"
	"github.com/containerd/typeurl/v2"
	"github.com/davecgh/go-spew/spew"
	selinux "github.com/opencontainers/selinux/go-selinux"
	"github.com/sirupsen/logrus"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	"github.com/containerd/containerd"
	containerdio "github.com/containerd/containerd/cio"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/pkg/cri/annotations"
	criconfig "github.com/containerd/containerd/pkg/cri/config"
	customopts "github.com/containerd/containerd/pkg/cri/opts"
	"github.com/containerd/containerd/pkg/cri/server/bandwidth"
	sandboxstore "github.com/containerd/containerd/pkg/cri/store/sandbox"
	"github.com/containerd/containerd/pkg/cri/util"
	"github.com/containerd/containerd/pkg/netns"
	"github.com/containerd/containerd/snapshots"
)

func init() {
	typeurl.Register(&sandboxstore.Metadata{},
		"github.com/containerd/cri/pkg/store/sandbox", "Metadata")
}

// RunPodSandbox creates and starts a pod-level sandbox. Runtimes should ensure
// the sandbox is in ready state.
func (c *criService) RunPodSandbox(ctx context.Context, r *runtime.RunPodSandboxRequest) (_ *runtime.RunPodSandboxResponse, retErr error) {
	config := r.GetConfig()
	log.G(ctx).Debugf("Sandbox config %+v", config)

	// Generate unique id and name for the sandbox and reserve the name.
	id := util.GenerateID()
	metadata := config.GetMetadata()
	if metadata == nil {
		return nil, errors.New("sandbox config must include metadata")
	}
	name := makeSandboxName(metadata)
	log.G(ctx).WithField("podsandboxid", id).Debugf("generated id for sandbox name %q", name)

	// cleanupErr records the last error returned by the critical cleanup operations in deferred functions,
	// like CNI teardown and stopping the running sandbox task.
	// If cleanup is not completed for some reason, the CRI-plugin will leave the sandbox
	// in a not-ready state, which can later be cleaned up by the next execution of the kubelet's syncPod workflow.
	var cleanupErr error

	// Reserve the sandbox name to avoid concurrent `RunPodSandbox` request starting the
	// same sandbox.
	if err := c.sandboxNameIndex.Reserve(name, id); err != nil {
		return nil, fmt.Errorf("failed to reserve sandbox name %q: %w", name, err)
	}
	defer func() {
		// Release the name if the function returns with an error and all the resource cleanup is done.
		// When cleanupErr != nil, the name will be cleaned in sandbox_remove.
		if retErr != nil && cleanupErr == nil {
			c.sandboxNameIndex.ReleaseByName(name)
		}
	}()

	// Create initial internal sandbox object.
	sandbox := sandboxstore.NewSandbox(
		sandboxstore.Metadata{
			ID:             id,
			Name:           name,
			Config:         config,
			RuntimeHandler: r.GetRuntimeHandler(),
		},
		sandboxstore.Status{
			State: sandboxstore.StateUnknown,
		},
	)

	// Ensure sandbox container image snapshot.
	image, err := c.ensureImageExists(ctx, c.config.SandboxImage, config)
	if err != nil {
		return nil, fmt.Errorf("failed to get sandbox image %q: %w", c.config.SandboxImage, err)
	}
	containerdImage, err := c.toContainerdImage(ctx, *image)
	if err != nil {
		return nil, fmt.Errorf("failed to get image from containerd %q: %w", image.ID, err)
	}

	ociRuntime, err := c.getSandboxRuntime(config, r.GetRuntimeHandler())
	if err != nil {
		return nil, fmt.Errorf("failed to get sandbox runtime: %w", err)
	}
	log.G(ctx).WithField("podsandboxid", id).Debugf("use OCI runtime %+v", ociRuntime)

	runtimeStart := time.Now()
	// Create sandbox container.
	// NOTE: sandboxContainerSpec SHOULD NOT have side
	// effect, e.g. accessing/creating files, so that we can test
	// it safely.
	// NOTE: the network namespace path will be created later and update through updateNetNamespacePath function
	spec, err := c.sandboxContainerSpec(id, config, &image.ImageSpec.Config, "", ociRuntime.PodAnnotations)
	if err != nil {
		return nil, fmt.Errorf("failed to generate sandbox container spec: %w", err)
	}
	log.G(ctx).WithField("podsandboxid", id).Debugf("sandbox container spec: %#+v", spew.NewFormatter(spec))
	sandbox.ProcessLabel = spec.Process.SelinuxLabel
	defer func() {
		if retErr != nil {
			selinux.ReleaseLabel(sandbox.ProcessLabel)
		}
	}()

	// handle any KVM based runtime
	if err := modifyProcessLabel(ociRuntime.Type, spec); err != nil {
		return nil, err
	}

	if config.GetLinux().GetSecurityContext().GetPrivileged() {
		// If privileged don't set selinux label, but we still record the MCS label so that
		// the unused label can be freed later.
		spec.Process.SelinuxLabel = ""
	}

	// Generate spec options that will be applied to the spec later.
	specOpts, err := c.sandboxContainerSpecOpts(config, &image.ImageSpec.Config)
	if err != nil {
		return nil, fmt.Errorf("failed to generate sandbox container spec options: %w", err)
	}

	sandboxLabels := buildLabels(config.Labels, image.ImageSpec.Config.Labels, containerKindSandbox)

	runtimeOpts, err := generateRuntimeOptions(ociRuntime, c.config)
	if err != nil {
		return nil, fmt.Errorf("failed to generate runtime options: %w", err)
	}

	sOpts := []snapshots.Opt{snapshots.WithLabels(snapshots.FilterInheritedLabels(config.Annotations))}
	extraSOpts, err := sandboxSnapshotterOpts(config)
	if err != nil {
		return nil, err
	}
	sOpts = append(sOpts, extraSOpts...)

	opts := []containerd.NewContainerOpts{
		containerd.WithSnapshotter(c.runtimeSnapshotter(ctx, ociRuntime)),
		customopts.WithNewSnapshot(id, containerdImage, sOpts...),
		containerd.WithSpec(spec, specOpts...),
		containerd.WithContainerLabels(sandboxLabels),
		containerd.WithContainerExtension(sandboxMetadataExtension, &sandbox.Metadata),
		containerd.WithRuntime(ociRuntime.Type, runtimeOpts)}

	container, err := c.client.NewContainer(ctx, id, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create containerd container: %w", err)
	}

	// Add container into sandbox store in INIT state.
	sandbox.Container = container

	defer func() {
		// Put the sandbox into sandbox store when the some resource fails to be cleaned.
		if retErr != nil && cleanupErr != nil {
			log.G(ctx).WithError(cleanupErr).Errorf("encountered an error cleaning up failed sandbox %q, marking sandbox state as SANDBOX_UNKNOWN", id)
			if err := c.sandboxStore.Add(sandbox); err != nil {
				log.G(ctx).WithError(err).Errorf("failed to add sandbox %+v into store", sandbox)
			}
		}
	}()

	defer func() {
		// Delete container only if all the resource cleanup is done.
		if retErr != nil && cleanupErr == nil {
			deferCtx, deferCancel := util.DeferContext()
			defer deferCancel()
			if cleanupErr = container.Delete(deferCtx, containerd.WithSnapshotCleanup); cleanupErr != nil {
				log.G(ctx).WithError(cleanupErr).Errorf("Failed to delete containerd container %q", id)
			}
		}
	}()

	// Create sandbox container root directories.
	sandboxRootDir := c.getSandboxRootDir(id)
	if err := c.os.MkdirAll(sandboxRootDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create sandbox root directory %q: %w",
			sandboxRootDir, err)
	}
	defer func() {
		if retErr != nil {
			// Cleanup the sandbox root directory.
			if err := c.os.RemoveAll(sandboxRootDir); err != nil {
				log.G(ctx).WithError(err).Errorf("Failed to remove sandbox root directory %q",
					sandboxRootDir)
			}
		}
	}()
	volatileSandboxRootDir := c.getVolatileSandboxRootDir(id)
	if err := c.os.MkdirAll(volatileSandboxRootDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create volatile sandbox root directory %q: %w",
			volatileSandboxRootDir, err)
	}
	defer func() {
		if retErr != nil {
			// Cleanup the volatile sandbox root directory.
			if err := c.os.RemoveAll(volatileSandboxRootDir); err != nil {
				log.G(ctx).WithError(err).Errorf("Failed to remove volatile sandbox root directory %q",
					volatileSandboxRootDir)
			}
		}
	}()

	// Setup files required for the sandbox.
	if err = c.setupSandboxFiles(id, config); err != nil {
		return nil, fmt.Errorf("failed to setup sandbox files: %w", err)
	}
	defer func() {
		if retErr != nil {
			if err = c.cleanupSandboxFiles(id, config); err != nil {
				log.G(ctx).WithError(err).Errorf("Failed to cleanup sandbox files in %q",
					sandboxRootDir)
			}
		}
	}()

	// Update sandbox created timestamp.
	info, err := container.Info(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get sandbox container info: %w", err)
	}

	userNsEnabled := false
	if goruntime.GOOS != "windows" {
		usernsOpts := config.GetLinux().GetSecurityContext().GetNamespaceOptions().GetUsernsOptions()
		if usernsOpts != nil && usernsOpts.GetMode() == runtime.NamespaceMode_POD {
			userNsEnabled = true
		}
	}

	// Setup the network namespace if host networking wasn't requested.
	if !hostNetwork(config) && !userNsEnabled {
		// XXX: We do c&p of this code later for the podNetwork && userNsEnabled case too.
		// We can't move this to a function, as the defer calls need to be executed if other
		// errors are returned in this function. So, we would need more refactors to move
		// this code to a function and the idea was to not change the current code for
		// !userNsEnabled case, therefore doing it would defeat the purpose.
		//
		// The difference between the cases is the use of netns.NewNetNS() vs
		// netns.NewNetNSFromPID() and we verify the task is still running in the other case.
		//
		// To simplify this, in the future, we should just remove this case (podNetwork &&
		// !userNsEnabled) and just keep the other case (podNetwork && userNsEnabled).
		netStart := time.Now()

		// If it is not in host network namespace then create a namespace and set the sandbox
		// handle. NetNSPath in sandbox metadata and NetNS is non empty only for non host network
		// namespaces. If the pod is in host network namespace then both are empty and should not
		// be used.
		var netnsMountDir = "/var/run/netns"
		if c.config.NetNSMountsUnderStateDir {
			netnsMountDir = filepath.Join(c.config.StateDir, "netns")
		}
		sandbox.NetNS, err = netns.NewNetNS(netnsMountDir)
		if err != nil {
			return nil, fmt.Errorf("failed to create network namespace for sandbox %q: %w", id, err)
		}
		sandbox.NetNSPath = sandbox.NetNS.GetPath()

		defer func() {
			// Remove the network namespace only if all the resource cleanup is done.
			if retErr != nil && cleanupErr == nil {
				if cleanupErr = sandbox.NetNS.Remove(); cleanupErr != nil {
					log.G(ctx).WithError(cleanupErr).Errorf("Failed to remove network namespace %s for sandbox %q", sandbox.NetNSPath, id)
					return
				}
				sandbox.NetNSPath = ""
			}
		}()

		// Update network namespace in the container's spec
		c.updateNetNamespacePath(spec, sandbox.NetNSPath)

		if err := container.Update(ctx,
			// Update spec of the container
			containerd.UpdateContainerOpts(containerd.WithSpec(spec)),
			// Update sandbox metadata to include NetNS info
			containerd.UpdateContainerOpts(containerd.WithContainerExtension(sandboxMetadataExtension, &sandbox.Metadata)),
		); err != nil {
			return nil, fmt.Errorf("failed to update the network namespace for the sandbox container %q: %w", id, err)
		}

		// Define this defer to teardownPodNetwork prior to the setupPodNetwork function call.
		// This is because in setupPodNetwork the resource is allocated even if it returns error, unlike other resource creation functions.
		defer func() {
			// Teardown the network only if all the resource cleanup is done.
			if retErr != nil && cleanupErr == nil {
				deferCtx, deferCancel := util.DeferContext()
				defer deferCancel()
				// Teardown network if an error is returned.
				if cleanupErr = c.teardownPodNetwork(deferCtx, sandbox); cleanupErr != nil {
					log.G(ctx).WithError(cleanupErr).Errorf("Failed to destroy network for sandbox %q", id)
				}
			}
		}()

		// Setup network for sandbox.
		// Certain VM based solutions like clear containers (Issue containerd/cri-containerd#524)
		// rely on the assumption that CRI shim will not be querying the network namespace to check the
		// network states such as IP.
		// In future runtime implementation should avoid relying on CRI shim implementation details.
		// In this case however caching the IP will add a subtle performance enhancement by avoiding
		// calls to network namespace of the pod to query the IP of the veth interface on every
		// SandboxStatus request.
		if err := c.setupPodNetwork(ctx, &sandbox); err != nil {
			return nil, fmt.Errorf("failed to setup network for sandbox %q: %w", id, err)
		}

		// Update metadata here to save CNI result and pod IPs to disk.
		if err := container.Update(ctx,
			// Update sandbox metadata to include NetNS info
			containerd.UpdateContainerOpts(containerd.WithContainerExtension(sandboxMetadataExtension, &sandbox.Metadata)),
		); err != nil {
			return nil, fmt.Errorf("failed to update the network namespace for the sandbox container %q: %w", id, err)
		}

		sandboxCreateNetworkTimer.UpdateSince(netStart)
	}

	// Create sandbox task in containerd.
	log.G(ctx).Tracef("Create sandbox container (id=%q, name=%q).",
		id, name)

	taskOpts := c.taskOpts(ociRuntime.Type)
	if ociRuntime.Path != "" {
		taskOpts = append(taskOpts, containerd.WithRuntimePath(ociRuntime.Path))
	}
	// We don't need stdio for sandbox container.
	task, err := container.NewTask(ctx, containerdio.NullIO, taskOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create containerd task: %w", err)
	}
	defer func() {
		if retErr != nil {
			deferCtx, deferCancel := util.DeferContext()
			defer deferCancel()
			// Cleanup the sandbox container if an error is returned.
			if _, err := task.Delete(deferCtx, containerd.WithProcessKill); err != nil && !errdefs.IsNotFound(err) {
				log.G(ctx).WithError(err).Errorf("Failed to delete sandbox container %q", id)
				cleanupErr = err
			}
		}
	}()

	// wait is a long running background request, no timeout needed.
	exitCh, err := task.Wait(util.NamespacedContext())
	if err != nil {
		return nil, fmt.Errorf("failed to wait for sandbox container task: %w", err)
	}

	if !hostNetwork(config) && userNsEnabled {
		// If userns is enabled, then the netns was created by the OCI runtime
		// when creating "task". The OCI runtime needs to create the netns
		// because, if userns is in use, the netns needs to be owned by the
		// userns. So, let the OCI runtime just handle this for us.
		// If the netns is not owned by the userns several problems will happen.
		// For instance, the container will lack permission (even if
		// capabilities are present) to modify the netns or, even worse, the OCI
		// runtime will fail to mount sysfs:
		// 	https://github.com/torvalds/linux/commit/7dc5dbc879bd0779924b5132a48b731a0bc04a1e#diff-4839664cd0c8eab716e064323c7cd71fR1164
		netStart := time.Now()

		// If it is not in host network namespace then create a namespace and set the sandbox
		// handle. NetNSPath in sandbox metadata and NetNS is non empty only for non host network
		// namespaces. If the pod is in host network namespace then both are empty and should not
		// be used.
		var netnsMountDir = "/var/run/netns"
		if c.config.NetNSMountsUnderStateDir {
			netnsMountDir = filepath.Join(c.config.StateDir, "netns")
		}
		sandbox.NetNS, err = netns.NewNetNSFromPID(netnsMountDir, task.Pid())
		if err != nil {
			return nil, fmt.Errorf("failed to create network namespace for sandbox %q: %w", id, err)
		}

		// Verify task is still in created state.
		if st, err := task.Status(ctx); err != nil || st.Status != containerd.Created {
			return nil, fmt.Errorf("failed to create pod sandbox %q: err is %v - status is %q and is expected %q", id, err, st.Status, containerd.Created)
		}
		sandbox.NetNSPath = sandbox.NetNS.GetPath()

		defer func() {
			// Remove the network namespace only if all the resource cleanup is done.
			if retErr != nil && cleanupErr == nil {
				if cleanupErr = sandbox.NetNS.Remove(); cleanupErr != nil {
					log.G(ctx).WithError(cleanupErr).Errorf("Failed to remove network namespace %s for sandbox %q", sandbox.NetNSPath, id)
					return
				}
				sandbox.NetNSPath = ""
			}
		}()

		// Update network namespace in the container's spec
		c.updateNetNamespacePath(spec, sandbox.NetNSPath)

		if err := container.Update(ctx,
			// Update spec of the container
			containerd.UpdateContainerOpts(containerd.WithSpec(spec)),
			// Update sandbox metadata to include NetNS info
			containerd.UpdateContainerOpts(containerd.WithContainerExtension(sandboxMetadataExtension, &sandbox.Metadata))); err != nil {
			return nil, fmt.Errorf("failed to update the network namespace for the sandbox container %q: %w", id, err)
		}

		// Define this defer to teardownPodNetwork prior to the setupPodNetwork function call.
		// This is because in setupPodNetwork the resource is allocated even if it returns error, unlike other resource creation functions.
		defer func() {
			// Teardown the network only if all the resource cleanup is done.
			if retErr != nil && cleanupErr == nil {
				deferCtx, deferCancel := util.DeferContext()
				defer deferCancel()
				// Teardown network if an error is returned.
				if cleanupErr = c.teardownPodNetwork(deferCtx, sandbox); cleanupErr != nil {
					log.G(ctx).WithError(cleanupErr).Errorf("Failed to destroy network for sandbox %q", id)
				}
			}
		}()

		// Setup network for sandbox.
		// Certain VM based solutions like clear containers (Issue containerd/cri-containerd#524)
		// rely on the assumption that CRI shim will not be querying the network namespace to check the
		// network states such as IP.
		// In future runtime implementation should avoid relying on CRI shim implementation details.
		// In this case however caching the IP will add a subtle performance enhancement by avoiding
		// calls to network namespace of the pod to query the IP of the veth interface on every
		// SandboxStatus request.
		if err := c.setupPodNetwork(ctx, &sandbox); err != nil {
			return nil, fmt.Errorf("failed to setup network for sandbox %q: %w", id, err)
		}

		sandboxCreateNetworkTimer.UpdateSince(netStart)
	}

	err = c.nri.RunPodSandbox(ctx, &sandbox)
	if err != nil {
		return nil, fmt.Errorf("NRI RunPodSandbox failed: %w", err)
	}

	defer func() {
		if retErr != nil {
			deferCtx, deferCancel := util.DeferContext()
			defer deferCancel()
			c.nri.RemovePodSandbox(deferCtx, &sandbox)
		}
	}()

	if err := task.Start(ctx); err != nil {
		return nil, fmt.Errorf("failed to start sandbox container task %q: %w", id, err)
	}

	if err := sandbox.Status.Update(func(status sandboxstore.Status) (sandboxstore.Status, error) {
		// Set the pod sandbox as ready after successfully start sandbox container.
		status.Pid = task.Pid()
		status.State = sandboxstore.StateReady
		status.CreatedAt = info.CreatedAt
		return status, nil
	}); err != nil {
		return nil, fmt.Errorf("failed to update sandbox status: %w", err)
	}

	if err := c.sandboxStore.Add(sandbox); err != nil {
		return nil, fmt.Errorf("failed to add sandbox %+v into store: %w", sandbox, err)
	}

	// Send CONTAINER_CREATED event with both ContainerId and SandboxId equal to SandboxId.
	// Note that this has to be done after sandboxStore.Add() because we need to get
	// SandboxStatus from the store and include it in the event.
	c.generateAndSendContainerEvent(ctx, id, id, runtime.ContainerEventType_CONTAINER_CREATED_EVENT)

	// start the monitor after adding sandbox into the store, this ensures
	// that sandbox is in the store, when event monitor receives the TaskExit event.
	//
	// TaskOOM from containerd may come before sandbox is added to store,
	// but we don't care about sandbox TaskOOM right now, so it is fine.
	c.eventMonitor.startSandboxExitMonitor(context.Background(), id, task.Pid(), exitCh)

	// Send CONTAINER_STARTED event with both ContainerId and SandboxId equal to SandboxId.
	c.generateAndSendContainerEvent(ctx, id, id, runtime.ContainerEventType_CONTAINER_STARTED_EVENT)

	sandboxRuntimeCreateTimer.WithValues(ociRuntime.Type).UpdateSince(runtimeStart)

	return &runtime.RunPodSandboxResponse{PodSandboxId: id}, nil
}

// getNetworkPlugin returns the network plugin to be used by the runtime class
// defaults to the global CNI options in the CRI config
func (c *criService) getNetworkPlugin(runtimeClass string) cni.CNI {
	if c.netPlugin == nil {
		return nil
	}
	i, ok := c.netPlugin[runtimeClass]
	if !ok {
		if i, ok = c.netPlugin[defaultNetworkPlugin]; !ok {
			return nil
		}
	}
	return i
}

// setupPodNetwork setups up the network for a pod
func (c *criService) setupPodNetwork(ctx context.Context, sandbox *sandboxstore.Sandbox) error {
	var (
		id        = sandbox.ID
		config    = sandbox.Config
		path      = sandbox.NetNSPath
		netPlugin = c.getNetworkPlugin(sandbox.RuntimeHandler)
		err       error
		result    *cni.Result
	)
	if netPlugin == nil {
		return errors.New("cni config not initialized")
	}

	opts, err := cniNamespaceOpts(id, config)
	if err != nil {
		return fmt.Errorf("get cni namespace options: %w", err)
	}
	log.G(ctx).WithField("podsandboxid", id).Debugf("begin cni setup")
	netStart := time.Now()
	if c.config.CniConfig.NetworkPluginSetupSerially {
		result, err = netPlugin.SetupSerially(ctx, id, path, opts...)
	} else {
		result, err = netPlugin.Setup(ctx, id, path, opts...)
	}
	networkPluginOperations.WithValues(networkSetUpOp).Inc()
	networkPluginOperationsLatency.WithValues(networkSetUpOp).UpdateSince(netStart)
	if err != nil {
		networkPluginOperationsErrors.WithValues(networkSetUpOp).Inc()
		return err
	}
	logDebugCNIResult(ctx, id, result)
	// Check if the default interface has IP config
	if configs, ok := result.Interfaces[defaultIfName]; ok && len(configs.IPConfigs) > 0 {
		sandbox.IP, sandbox.AdditionalIPs = selectPodIPs(ctx, configs.IPConfigs, c.config.IPPreference)
		sandbox.CNIResult = result
		return nil
	}
	return fmt.Errorf("failed to find network info for sandbox %q", id)
}

// cniNamespaceOpts get CNI namespace options from sandbox config.
func cniNamespaceOpts(id string, config *runtime.PodSandboxConfig) ([]cni.NamespaceOpts, error) {
	opts := []cni.NamespaceOpts{
		cni.WithLabels(toCNILabels(id, config)),
		cni.WithCapability(annotations.PodAnnotations, config.Annotations),
	}

	portMappings := toCNIPortMappings(config.GetPortMappings())
	if len(portMappings) > 0 {
		opts = append(opts, cni.WithCapabilityPortMap(portMappings))
	}

	// Will return an error if the bandwidth limitation has the wrong unit
	// or an unreasonable value see validateBandwidthIsReasonable()
	bandWidth, err := toCNIBandWidth(config.Annotations)
	if err != nil {
		return nil, err
	}
	if bandWidth != nil {
		opts = append(opts, cni.WithCapabilityBandWidth(*bandWidth))
	}

	dns := toCNIDNS(config.GetDnsConfig())
	if dns != nil {
		opts = append(opts, cni.WithCapabilityDNS(*dns))
	}

	if cgroup := config.GetLinux().GetCgroupParent(); cgroup != "" {
		opts = append(opts, cni.WithCapabilityCgroupPath(cgroup))
	}

	return opts, nil
}

// toCNILabels adds pod metadata into CNI labels.
func toCNILabels(id string, config *runtime.PodSandboxConfig) map[string]string {
	return map[string]string{
		"K8S_POD_NAMESPACE":          config.GetMetadata().GetNamespace(),
		"K8S_POD_NAME":               config.GetMetadata().GetName(),
		"K8S_POD_INFRA_CONTAINER_ID": id,
		"K8S_POD_UID":                config.GetMetadata().GetUid(),
		"IgnoreUnknown":              "1",
	}
}

// toCNIBandWidth converts CRI annotations to CNI bandwidth.
func toCNIBandWidth(annotations map[string]string) (*cni.BandWidth, error) {
	ingress, egress, err := bandwidth.ExtractPodBandwidthResources(annotations)
	if err != nil {
		return nil, fmt.Errorf("reading pod bandwidth annotations: %w", err)
	}

	if ingress == nil && egress == nil {
		return nil, nil
	}

	bandWidth := &cni.BandWidth{}

	if ingress != nil {
		bandWidth.IngressRate = uint64(ingress.Value())
		bandWidth.IngressBurst = math.MaxUint32
	}

	if egress != nil {
		bandWidth.EgressRate = uint64(egress.Value())
		bandWidth.EgressBurst = math.MaxUint32
	}

	return bandWidth, nil
}

// toCNIPortMappings converts CRI port mappings to CNI.
func toCNIPortMappings(criPortMappings []*runtime.PortMapping) []cni.PortMapping {
	var portMappings []cni.PortMapping
	for _, mapping := range criPortMappings {
		if mapping.HostPort <= 0 {
			continue
		}
		portMappings = append(portMappings, cni.PortMapping{
			HostPort:      mapping.HostPort,
			ContainerPort: mapping.ContainerPort,
			Protocol:      strings.ToLower(mapping.Protocol.String()),
			HostIP:        mapping.HostIp,
		})
	}
	return portMappings
}

// toCNIDNS converts CRI DNSConfig to CNI.
func toCNIDNS(dns *runtime.DNSConfig) *cni.DNS {
	if dns == nil {
		return nil
	}
	return &cni.DNS{
		Servers:  dns.GetServers(),
		Searches: dns.GetSearches(),
		Options:  dns.GetOptions(),
	}
}

// selectPodIPs select an ip from the ip list.
func selectPodIPs(ctx context.Context, configs []*cni.IPConfig, preference string) (string, []string) {
	if len(configs) == 1 {
		return ipString(configs[0]), nil
	}
	toStrings := func(ips []*cni.IPConfig) (o []string) {
		for _, i := range ips {
			o = append(o, ipString(i))
		}
		return o
	}
	var extra []string
	switch preference {
	default:
		if preference != "ipv4" && preference != "" {
			log.G(ctx).WithField("ip_pref", preference).Warn("invalid ip_pref, falling back to ipv4")
		}
		for i, ip := range configs {
			if ip.IP.To4() != nil {
				return ipString(ip), append(extra, toStrings(configs[i+1:])...)
			}
			extra = append(extra, ipString(ip))
		}
	case "ipv6":
		for i, ip := range configs {
			if ip.IP.To16() != nil {
				return ipString(ip), append(extra, toStrings(configs[i+1:])...)
			}
			extra = append(extra, ipString(ip))
		}
	case "cni":
		// use func default return
	}

	all := toStrings(configs)
	return all[0], all[1:]
}

func ipString(ip *cni.IPConfig) string {
	return ip.IP.String()
}

// untrustedWorkload returns true if the sandbox contains untrusted workload.
func untrustedWorkload(config *runtime.PodSandboxConfig) bool {
	return config.GetAnnotations()[annotations.UntrustedWorkload] == "true"
}

// hostAccessingSandbox returns true if the sandbox configuration
// requires additional host access for the sandbox.
func hostAccessingSandbox(config *runtime.PodSandboxConfig) bool {
	securityContext := config.GetLinux().GetSecurityContext()

	namespaceOptions := securityContext.GetNamespaceOptions()
	if namespaceOptions.GetNetwork() == runtime.NamespaceMode_NODE ||
		namespaceOptions.GetPid() == runtime.NamespaceMode_NODE ||
		namespaceOptions.GetIpc() == runtime.NamespaceMode_NODE {
		return true
	}

	return false
}

// getSandboxRuntime returns the runtime configuration for sandbox.
// If the sandbox contains untrusted workload, runtime for untrusted workload will be returned,
// or else default runtime will be returned.
func (c *criService) getSandboxRuntime(config *runtime.PodSandboxConfig, runtimeHandler string) (criconfig.Runtime, error) {
	if untrustedWorkload(config) {
		// If the untrusted annotation is provided, runtimeHandler MUST be empty.
		if runtimeHandler != "" && runtimeHandler != criconfig.RuntimeUntrusted {
			return criconfig.Runtime{}, errors.New("untrusted workload with explicit runtime handler is not allowed")
		}

		//  If the untrusted workload is requesting access to the host/node, this request will fail.
		//
		//  Note: If the workload is marked untrusted but requests privileged, this can be granted, as the
		// runtime may support this.  For example, in a virtual-machine isolated runtime, privileged
		// is a supported option, granting the workload to access the entire guest VM instead of host.
		// TODO(windows): Deprecate this so that we don't need to handle it for windows.
		if hostAccessingSandbox(config) {
			return criconfig.Runtime{}, errors.New("untrusted workload with host access is not allowed")
		}

		runtimeHandler = criconfig.RuntimeUntrusted
	}

	if runtimeHandler == "" {
		runtimeHandler = c.config.ContainerdConfig.DefaultRuntimeName
	}

	handler, ok := c.config.ContainerdConfig.Runtimes[runtimeHandler]
	if !ok {
		return criconfig.Runtime{}, fmt.Errorf("no runtime for %q is configured", runtimeHandler)
	}
	return handler, nil
}

func logDebugCNIResult(ctx context.Context, sandboxID string, result *cni.Result) {
	if logrus.GetLevel() < logrus.DebugLevel {
		return
	}
	cniResult, err := json.Marshal(result)
	if err != nil {
		log.G(ctx).WithField("podsandboxid", sandboxID).WithError(err).Errorf("Failed to marshal CNI result: %v", err)
		return
	}
	log.G(ctx).WithField("podsandboxid", sandboxID).Debugf("cni result: %s", string(cniResult))
}
