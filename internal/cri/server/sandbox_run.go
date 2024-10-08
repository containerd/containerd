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
	"strings"
	"time"

	"github.com/containerd/go-cni"
	"github.com/containerd/log"
	"github.com/containerd/typeurl/v2"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	sb "github.com/containerd/containerd/v2/core/sandbox"
	"github.com/containerd/containerd/v2/internal/cri/annotations"
	"github.com/containerd/containerd/v2/internal/cri/bandwidth"
	criconfig "github.com/containerd/containerd/v2/internal/cri/config"
	"github.com/containerd/containerd/v2/internal/cri/server/podsandbox"
	sandboxstore "github.com/containerd/containerd/v2/internal/cri/store/sandbox"
	"github.com/containerd/containerd/v2/internal/cri/util"
	"github.com/containerd/containerd/v2/pkg/netns"
	"github.com/containerd/containerd/v2/pkg/tracing"
)

func init() {
	typeurl.Register(&sandboxstore.Metadata{},
		"github.com/containerd/cri/pkg/store/sandbox", "Metadata")
}

// RunPodSandbox creates and starts a pod-level sandbox. Runtimes should ensure
// the sandbox is in ready state.
func (c *criService) RunPodSandbox(ctx context.Context, r *runtime.RunPodSandboxRequest) (_ *runtime.RunPodSandboxResponse, retErr error) {
	span := tracing.SpanFromContext(ctx)
	config := r.GetConfig()
	log.G(ctx).Debugf("Sandbox config %+v", config)

	// Generate unique id and name for the sandbox and reserve the name.
	id := util.GenerateID()
	metadata := config.GetMetadata()
	if metadata == nil {
		return nil, errors.New("sandbox config must include metadata")
	}
	name := makeSandboxName(metadata)

	span.SetAttributes(
		tracing.Attribute("sandbox.id", id),
		tracing.Attribute("sandbox.name", name),
	)
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
		// Release the name if the function returns with an error.
		// When cleanupErr != nil, the name will be cleaned in sandbox_remove.
		if retErr != nil && cleanupErr == nil {
			c.sandboxNameIndex.ReleaseByName(name)
		}
	}()

	var (
		err         error
		sandboxInfo = sb.Sandbox{ID: id}
	)

	ociRuntime, err := c.config.GetSandboxRuntime(config, r.GetRuntimeHandler())
	if err != nil {
		return nil, fmt.Errorf("unable to get OCI runtime for sandbox %q: %w", id, err)
	}

	sandboxInfo.Runtime.Name = ociRuntime.Type
	sandboxInfo.Sandboxer = ociRuntime.Sandboxer

	runtimeStart := time.Now()
	// Retrieve runtime options
	runtimeOpts, err := criconfig.GenerateRuntimeOptions(ociRuntime)
	if err != nil {
		return nil, fmt.Errorf("failed to generate sandbox runtime options: %w", err)
	}

	if runtimeOpts != nil {
		sandboxInfo.Runtime.Options, err = typeurl.MarshalAny(runtimeOpts)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal runtime options: %w", err)
		}
	}

	// Save sandbox name
	sandboxInfo.AddLabel("name", name)

	// Create initial internal sandbox object.
	sandbox := sandboxstore.NewSandbox(
		sandboxstore.Metadata{
			ID:             id,
			Name:           name,
			Config:         config,
			RuntimeHandler: r.GetRuntimeHandler(),
		},
		sandboxstore.Status{
			State:     sandboxstore.StateUnknown,
			CreatedAt: time.Now().UTC(),
		},
	)
	sandbox.Sandboxer = ociRuntime.Sandboxer

	if _, err := c.client.SandboxStore().Create(ctx, sandboxInfo); err != nil {
		return nil, fmt.Errorf("failed to save sandbox metadata: %w", err)
	}
	defer func() {
		if retErr != nil && cleanupErr == nil {
			cleanupErr = c.client.SandboxStore().Delete(ctx, id)
		}
	}()

	defer func() {
		// Put the sandbox into sandbox store when some resources fail to be cleaned.
		if retErr != nil && cleanupErr != nil {
			log.G(ctx).WithError(cleanupErr).Errorf("encountered an error cleaning up failed sandbox %q, marking sandbox state as SANDBOX_UNKNOWN", id)
			if err := c.sandboxStore.Add(sandbox); err != nil {
				log.G(ctx).WithError(err).Errorf("failed to add sandbox %+v into store", sandbox)
			}
		}
	}()

	// XXX: What we really want here is to call controller.Platform() and then check
	// platform.OS, but that is only populated after controller.Create() and that needs to be
	// done later (uses sandbox.NSPath that we will set just _after_ this).
	// So, lets check for the Linux section on the config, if that is populated, we assume the
	// platform is linux.
	// This is a hack, we should improve the controller interface to return the platform
	// earlier. But should work fine for this specific use.
	userNsEnabled := false
	if linux := config.GetLinux(); linux != nil {
		usernsOpts := linux.GetSecurityContext().GetNamespaceOptions().GetUsernsOptions()
		if usernsOpts != nil && usernsOpts.GetMode() == runtime.NamespaceMode_POD {
			userNsEnabled = true
		}
	}

	// Setup the network namespace if host networking wasn't requested.
	if !hostNetwork(config) {
		span.AddEvent("setup pod network")
		netStart := time.Now()
		// If it is not in host network namespace then create a namespace and set the sandbox
		// handle. NetNSPath in sandbox metadata and NetNS is non empty only for non host network
		// namespaces. If the pod is in host network namespace then both are empty and should not
		// be used.
		var netnsMountDir = "/var/run/netns"
		if c.config.NetNSMountsUnderStateDir {
			netnsMountDir = filepath.Join(c.config.StateDir, "netns")
		}

		if !userNsEnabled {
			sandbox.NetNS, err = netns.NewNetNS(netnsMountDir)
		} else {
			usernsOpts := config.GetLinux().GetSecurityContext().GetNamespaceOptions().GetUsernsOptions()
			sandbox.NetNS, err = c.setupNetnsWithinUserns(netnsMountDir, usernsOpts)
		}
		if err != nil {
			return nil, fmt.Errorf("failed to create network namespace for sandbox %q: %w", id, err)
		}
		// Update network namespace in the store, which is used to generate the container's spec
		sandbox.NetNSPath = sandbox.NetNS.GetPath()
		defer func() {
			// Remove the network namespace only if all the resource cleanup is done
			if retErr != nil && cleanupErr == nil {
				if cleanupErr = sandbox.NetNS.Remove(); cleanupErr != nil {
					log.G(ctx).WithError(cleanupErr).Errorf("Failed to remove network namespace %s for sandbox %q", sandbox.NetNSPath, id)
					return
				}
				sandbox.NetNSPath = ""
			}
		}()

		if err := sandboxInfo.AddExtension(podsandbox.MetadataKey, &sandbox.Metadata); err != nil {
			return nil, fmt.Errorf("unable to save sandbox %q to store: %w", id, err)
		}
		// Save sandbox metadata to store
		if sandboxInfo, err = c.client.SandboxStore().Update(ctx, sandboxInfo, "extensions"); err != nil {
			return nil, fmt.Errorf("unable to update extensions for sandbox %q: %w", id, err)
		}

		// Define this defer to teardownPodNetwork prior to the setupPodNetwork function call.
		// This is because in setupPodNetwork the resource is allocated even if it returns error, unlike other resource
		// creation functions.
		defer func() {
			// Remove the network namespace only if all the resource cleanup is done.
			if retErr != nil && cleanupErr == nil {
				deferCtx, deferCancel := util.DeferContext()
				defer deferCancel()
				// Teardown network if an error is returned.
				if cleanupErr = c.teardownPodNetwork(deferCtx, sandbox); cleanupErr != nil {
					log.G(ctx).WithError(cleanupErr).Errorf("Failed to destroy network for sandbox %q", id)

					// ignoring failed to destroy networks when we failed to setup networks
					if sandbox.CNIResult == nil {
						cleanupErr = nil
					}
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

	if err := sandboxInfo.AddExtension(podsandbox.MetadataKey, &sandbox.Metadata); err != nil {
		return nil, fmt.Errorf("unable to save sandbox %q to store: %w", id, err)
	}

	// Save sandbox metadata to store
	if sandboxInfo, err = c.client.SandboxStore().Update(ctx, sandboxInfo, "extensions"); err != nil {
		return nil, fmt.Errorf("unable to update extensions for sandbox %q: %w", id, err)
	}

	if err := c.sandboxService.CreateSandbox(ctx, sandboxInfo, sb.WithOptions(config), sb.WithNetNSPath(sandbox.NetNSPath)); err != nil {
		return nil, fmt.Errorf("failed to create sandbox %q: %w", id, err)
	}

	ctrl, err := c.sandboxService.StartSandbox(ctx, sandbox.Sandboxer, id)
	if err != nil {
		var cerr podsandbox.CleanupErr
		if errors.As(err, &cerr) {
			cleanupErr = fmt.Errorf("failed to cleanup sandbox: %w", cerr)

			// Strip last error as cleanup error to handle separately
			if merr, ok := err.(interface{ Unwrap() []error }); ok {
				if errs := merr.Unwrap(); len(errs) > 0 {
					err = errs[0]
				}
			}
		}
		return nil, fmt.Errorf("failed to start sandbox %q: %w", id, err)
	}

	if ctrl.Address != "" {
		sandbox.Endpoint = sandboxstore.Endpoint{
			Version: ctrl.Version,
			Address: ctrl.Address,
		}
	}

	if sandboxInfo, err = c.client.SandboxStore().Update(ctx, sandboxInfo, "extensions"); err != nil {
		return nil, fmt.Errorf("unable to update extensions for sandbox %q: %w", id, err)
	}

	// TODO: get rid of this. sandbox object should no longer have Container field.
	if ociRuntime.Sandboxer == string(criconfig.ModePodSandbox) {
		container, err := c.client.LoadContainer(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("failed to load container %q for sandbox: %w", id, err)
		}
		sandbox.Container = container
	}

	labels := ctrl.Labels
	if labels == nil {
		labels = map[string]string{}
	}

	sandbox.ProcessLabel = labels["selinux_label"]

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

	if err := sandbox.Status.Update(func(status sandboxstore.Status) (sandboxstore.Status, error) {
		// Set the pod sandbox as ready after successfully start sandbox container.
		status.Pid = ctrl.Pid
		status.State = sandboxstore.StateReady
		status.CreatedAt = ctrl.CreatedAt
		return status, nil
	}); err != nil {
		return nil, fmt.Errorf("failed to update sandbox status: %w", err)
	}

	// Add sandbox into sandbox store in INIT state.
	if err := c.sandboxStore.Add(sandbox); err != nil {
		return nil, fmt.Errorf("failed to add sandbox %+v into store: %w", sandbox, err)
	}

	// Send CONTAINER_CREATED event with both ContainerId and SandboxId equal to SandboxId.
	// Note that this has to be done after sandboxStore.Add() because we need to get
	// SandboxStatus from the store and include it in the event.
	c.generateAndSendContainerEvent(ctx, id, id, runtime.ContainerEventType_CONTAINER_CREATED_EVENT)

	exitCh, err := c.sandboxService.WaitSandbox(util.NamespacedContext(), sandbox.Sandboxer, id)
	if err != nil {
		return nil, fmt.Errorf("failed to wait sandbox %s: %v", id, err)
	}

	// start the monitor after adding sandbox into the store, this ensures
	// that sandbox is in the store, when event monitor receives the TaskExit event.
	//
	// TaskOOM from containerd may come before sandbox is added to store,
	// but we don't care about sandbox TaskOOM right now, so it is fine.
	c.startSandboxExitMonitor(context.Background(), id, exitCh)

	// Send CONTAINER_STARTED event with ContainerId equal to SandboxId.
	c.generateAndSendContainerEvent(ctx, id, id, runtime.ContainerEventType_CONTAINER_STARTED_EVENT)

	sandboxRuntimeCreateTimer.WithValues(labels["oci_runtime_type"]).UpdateSince(runtimeStart)

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
	if c.config.UseInternalLoopback {
		err := c.bringUpLoopback(path)
		if err != nil {
			return fmt.Errorf("unable to set lo to up: %w", err)
		}
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
			if ip.IP.To4() == nil {
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

func logDebugCNIResult(ctx context.Context, sandboxID string, result *cni.Result) {
	if log.GetLevel() < log.DebugLevel {
		return
	}
	cniResult, err := json.Marshal(result)
	if err != nil {
		log.G(ctx).WithField("podsandboxid", sandboxID).WithError(err).Errorf("Failed to marshal CNI result: %v", err)
		return
	}
	log.G(ctx).WithField("podsandboxid", sandboxID).Debugf("cni result: %s", string(cniResult))
}
