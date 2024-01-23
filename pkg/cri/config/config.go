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

package config

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/containerd/log"
	"github.com/pelletier/go-toml/v2"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	runhcsoptions "github.com/Microsoft/hcsshim/cmd/containerd-shim-runhcs-v1/options"
	runcoptions "github.com/containerd/containerd/v2/core/runtime/v2/runc/options"
	"github.com/containerd/containerd/v2/pkg/cri/annotations"
	"github.com/containerd/containerd/v2/pkg/deprecation"
	runtimeoptions "github.com/containerd/containerd/v2/pkg/runtimeoptions/v1"
	"github.com/containerd/containerd/v2/plugins"
)

const (
	// defaultImagePullProgressTimeoutDuration is the default value of imagePullProgressTimeout.
	//
	// NOTE:
	//
	// This ImagePullProgressTimeout feature is ported from kubelet/dockershim's
	// --image-pull-progress-deadline. The original value is 1m0. Unlike docker
	// daemon, the containerd doesn't have global concurrent download limitation
	// before migrating to Transfer Service. If kubelet runs with concurrent
	// image pull, the node will run under IO pressure. The ImagePull process
	// could be impacted by self, if the target image is large one with a
	// lot of layers. And also both container's writable layers and image's storage
	// share one disk. The ImagePull process commits blob to content store
	// with fsync, which might bring the unrelated files' dirty pages into
	// disk in one transaction [1]. The 1m0 value isn't good enough. Based
	// on #9347 case and kubernetes community's usage [2], the default value
	// is updated to 5m0. If end-user still runs into unexpected cancel,
	// they need to config it based on their environment.
	//
	// [1]: Fast commits for ext4 - https://lwn.net/Articles/842385/
	// [2]: https://github.com/kubernetes/kubernetes/blob/1635c380b26a1d8cc25d36e9feace9797f4bae3c/cluster/gce/util.sh#L882
	defaultImagePullProgressTimeoutDuration = 5 * time.Minute
)

type SandboxControllerMode string

const (
	// ModePodSandbox means use Controller implementation from sbserver podsandbox package.
	// We take this one as a default mode.
	ModePodSandbox SandboxControllerMode = "podsandbox"
	// ModeShim means use whatever Controller implementation provided by shim.
	ModeShim SandboxControllerMode = "shim"
	// DefaultSandboxImage is the default image to use for sandboxes when empty or
	// for default configurations.
	DefaultSandboxImage = "registry.k8s.io/pause:3.9"
)

// Runtime struct to contain the type(ID), engine, and root variables for a default runtime
// and a runtime for untrusted workload.
type Runtime struct {
	// Type is the runtime type to use in containerd e.g. io.containerd.runtime.v1.linux
	Type string `toml:"runtime_type" json:"runtimeType"`
	// Path is an optional field that can be used to overwrite path to a shim runtime binary.
	// When specified, containerd will ignore runtime name field when resolving shim location.
	// Path must be abs.
	Path string `toml:"runtime_path" json:"runtimePath"`
	// PodAnnotations is a list of pod annotations passed to both pod sandbox as well as
	// container OCI annotations.
	PodAnnotations []string `toml:"pod_annotations" json:"PodAnnotations"`
	// ContainerAnnotations is a list of container annotations passed through to the OCI config of the containers.
	// Container annotations in CRI are usually generated by other Kubernetes node components (i.e., not users).
	// Currently, only device plugins populate the annotations.
	ContainerAnnotations []string `toml:"container_annotations" json:"ContainerAnnotations"`
	// Options are config options for the runtime.
	Options map[string]interface{} `toml:"options" json:"options"`
	// PrivilegedWithoutHostDevices overloads the default behaviour for adding host devices to the
	// runtime spec when the container is privileged. Defaults to false.
	PrivilegedWithoutHostDevices bool `toml:"privileged_without_host_devices" json:"privileged_without_host_devices"`
	// PrivilegedWithoutHostDevicesAllDevicesAllowed overloads the default behaviour device allowlisting when
	// to the runtime spec when the container when PrivilegedWithoutHostDevices is already enabled. Requires
	// PrivilegedWithoutHostDevices to be enabled. Defaults to false.
	PrivilegedWithoutHostDevicesAllDevicesAllowed bool `toml:"privileged_without_host_devices_all_devices_allowed" json:"privileged_without_host_devices_all_devices_allowed"`
	// BaseRuntimeSpec is a json file with OCI spec to use as base spec that all container's will be created from.
	BaseRuntimeSpec string `toml:"base_runtime_spec" json:"baseRuntimeSpec"`
	// NetworkPluginConfDir is a directory containing the CNI network information for the runtime class.
	NetworkPluginConfDir string `toml:"cni_conf_dir" json:"cniConfDir"`
	// NetworkPluginMaxConfNum is the max number of plugin config files that will
	// be loaded from the cni config directory by go-cni. Set the value to 0 to
	// load all config files (no arbitrary limit). The legacy default value is 1.
	NetworkPluginMaxConfNum int `toml:"cni_max_conf_num" json:"cniMaxConfNum"`
	// Snapshotter setting snapshotter at runtime level instead of making it as a global configuration.
	// An example use case is to use devmapper or other snapshotters in Kata containers for performance and security
	// while using default snapshotters for operational simplicity.
	// See https://github.com/containerd/containerd/issues/6657 for details.
	Snapshotter string `toml:"snapshotter" json:"snapshotter"`
	// Sandboxer defines which sandbox runtime to use when scheduling pods
	// This features requires the new CRI server implementation (enabled by default in 2.0)
	// shim - means use whatever Controller implementation provided by shim (e.g. use RemoteController).
	// podsandbox - means use Controller implementation from sbserver podsandbox package.
	Sandboxer string `toml:"sandboxer" json:"sandboxer"`
}

// ContainerdConfig contains toml config related to containerd
type ContainerdConfig struct {
	// DefaultRuntimeName is the default runtime name to use from the runtimes table.
	DefaultRuntimeName string `toml:"default_runtime_name" json:"defaultRuntimeName"`

	// Runtimes is a map from CRI RuntimeHandler strings, which specify types of runtime
	// configurations, to the matching configurations.
	Runtimes map[string]Runtime `toml:"runtimes" json:"runtimes"`

	// IgnoreBlockIONotEnabledErrors is a boolean flag to ignore
	// blockio related errors when blockio support has not been
	// enabled.
	IgnoreBlockIONotEnabledErrors bool `toml:"ignore_blockio_not_enabled_errors" json:"ignoreBlockIONotEnabledErrors"`

	// IgnoreRdtNotEnabledErrors is a boolean flag to ignore RDT related errors
	// when RDT support has not been enabled.
	IgnoreRdtNotEnabledErrors bool `toml:"ignore_rdt_not_enabled_errors" json:"ignoreRdtNotEnabledErrors"`
}

// CniConfig contains toml config related to cni
type CniConfig struct {
	// NetworkPluginBinDir is the directory in which the binaries for the plugin is kept.
	NetworkPluginBinDir string `toml:"bin_dir" json:"binDir"`
	// NetworkPluginConfDir is the directory in which the admin places a CNI conf.
	NetworkPluginConfDir string `toml:"conf_dir" json:"confDir"`
	// NetworkPluginMaxConfNum is the max number of plugin config files that will
	// be loaded from the cni config directory by go-cni. Set the value to 0 to
	// load all config files (no arbitrary limit). The legacy default value is 1.
	NetworkPluginMaxConfNum int `toml:"max_conf_num" json:"maxConfNum"`
	// NetworkPluginSetupSerially is a boolean flag to specify whether containerd sets up networks serially
	// if there are multiple CNI plugin config files existing and NetworkPluginMaxConfNum is larger than 1.
	//
	// NOTE: On the Linux platform, containerd provides loopback network
	// configuration by default. There are at least two network plugins.
	// The default value of NetworkPluginSetupSerially is false which means
	// the loopback and eth0 are handled in parallel mode. Since the loopback
	// device is created as the net namespace is created, it's safe to run
	// in parallel mode as the default setting.
	NetworkPluginSetupSerially bool `toml:"setup_serially" json:"setupSerially"`
	// NetworkPluginConfTemplate is the file path of golang template used to generate cni config.
	// When it is set, containerd will get cidr(s) from kubelet to replace {{.PodCIDR}},
	// {{.PodCIDRRanges}} or {{.Routes}} in the template, and write the config into
	// NetworkPluginConfDir.
	// Ideally the cni config should be placed by system admin or cni daemon like calico,
	// weaveworks etc. However, this is useful for the cases when there is no cni daemonset to place cni config.
	// This allowed for very simple generic networking using the Kubernetes built in node pod CIDR IPAM, avoiding the
	// need to fetch the node object through some external process (which has scalability, auth, complexity issues).
	// It is currently heavily used in kubernetes-containerd CI testing
	// NetworkPluginConfTemplate was once deprecated in containerd v1.7.0,
	// but its deprecation was cancelled in v1.7.3.
	NetworkPluginConfTemplate string `toml:"conf_template" json:"confTemplate"`
	// IPPreference specifies the strategy to use when selecting the main IP address for a pod.
	//
	// Options include:
	// * ipv4, "" - (default) select the first ipv4 address
	// * ipv6 - select the first ipv6 address
	// * cni - use the order returned by the CNI plugins, returning the first IP address from the results
	IPPreference string `toml:"ip_pref" json:"ipPref"`
}

// Mirror contains the config related to the registry mirror
type Mirror struct {
	// Endpoints are endpoints for a namespace. CRI plugin will try the endpoints
	// one by one until a working one is found. The endpoint must be a valid url
	// with host specified.
	// The scheme, host and path from the endpoint URL will be used.
	Endpoints []string `toml:"endpoint" json:"endpoint"`
}

// AuthConfig contains the config related to authentication to a specific registry
type AuthConfig struct {
	// Username is the username to login the registry.
	Username string `toml:"username" json:"username"`
	// Password is the password to login the registry.
	Password string `toml:"password" json:"password"`
	// Auth is a base64 encoded string from the concatenation of the username,
	// a colon, and the password.
	Auth string `toml:"auth" json:"auth"`
	// IdentityToken is used to authenticate the user and get
	// an access token for the registry.
	IdentityToken string `toml:"identitytoken" json:"identitytoken"`
}

// Registry is registry settings configured
type Registry struct {
	// ConfigPath is a path to the root directory containing registry-specific
	// configurations.
	// If ConfigPath is set, the rest of the registry specific options are ignored.
	ConfigPath string `toml:"config_path" json:"configPath"`
	// Mirrors are namespace to mirror mapping for all namespaces.
	// This option will not be used when ConfigPath is provided.
	// DEPRECATED: Use ConfigPath instead. Remove in containerd 2.0.
	Mirrors map[string]Mirror `toml:"mirrors" json:"mirrors"`
	// Configs are configs for each registry.
	// The key is the domain name or IP of the registry.
	// DEPRECATED: Use ConfigPath instead.
	Configs map[string]RegistryConfig `toml:"configs" json:"configs"`
	// Auths are registry endpoint to auth config mapping. The registry endpoint must
	// be a valid url with host specified.
	// DEPRECATED: Use ConfigPath instead. Remove in containerd 2.0, supported in 1.x releases.
	Auths map[string]AuthConfig `toml:"auths" json:"auths"`
	// Headers adds additional HTTP headers that get sent to all registries
	Headers map[string][]string `toml:"headers" json:"headers"`
}

// RegistryConfig contains configuration used to communicate with the registry.
type RegistryConfig struct {
	// Auth contains information to authenticate to the registry.
	Auth *AuthConfig `toml:"auth" json:"auth"`
}

// ImageDecryption contains configuration to handling decryption of encrypted container images.
type ImageDecryption struct {
	// KeyModel specifies the trust model of where keys should reside.
	//
	// Details of field usage can be found in:
	// https://github.com/containerd/containerd/tree/main/docs/cri/config.md
	//
	// Details of key models can be found in:
	// https://github.com/containerd/containerd/tree/main/docs/cri/decryption.md
	KeyModel string `toml:"key_model" json:"keyModel"`
}

// ImagePlatform represents the platform to use for an image including the
// snapshotter to use. If snapshotter is not provided, the platform default
// can be assumed. When platform is not provided, the default platform can
// be assumed
type ImagePlatform struct {
	Platform string `toml:"platform" json:"platform"`
	// Snapshotter setting snapshotter at runtime level instead of making it as a global configuration.
	// An example use case is to use devmapper or other snapshotters in Kata containers for performance and security
	// while using default snapshotters for operational simplicity.
	// See https://github.com/containerd/containerd/issues/6657 for details.
	Snapshotter string `toml:"snapshotter" json:"snapshotter"`
}

type ImageConfig struct {
	// Snapshotter is the snapshotter used by containerd.
	Snapshotter string `toml:"snapshotter" json:"snapshotter"`

	// DisableSnapshotAnnotations disables to pass additional annotations (image
	// related information) to snapshotters. These annotations are required by
	// stargz snapshotter (https://github.com/containerd/stargz-snapshotter).
	DisableSnapshotAnnotations bool `toml:"disable_snapshot_annotations" json:"disableSnapshotAnnotations"`

	// DiscardUnpackedLayers is a boolean flag to specify whether to allow GC to
	// remove layers from the content store after successfully unpacking these
	// layers to the snapshotter.
	DiscardUnpackedLayers bool `toml:"discard_unpacked_layers" json:"discardUnpackedLayers"`

	// PinnedImages are images which the CRI plugin uses and should not be
	// removed by the CRI client. The images have a key which can be used
	// by other plugins to lookup the current image name.
	// Image names should be full names including domain and tag
	// Examples:
	//   "sandbox": "k8s.gcr.io/pause:3.9"
	//   "base": "docker.io/library/ubuntu:latest"
	// Migrated from:
	// (PluginConfig).SandboxImage string `toml:"sandbox_image" json:"sandboxImage"`
	PinnedImages map[string]string

	// RuntimePlatforms is map between the runtime and the image platform to
	// use for that runtime. When resolving an image for a runtime, this
	// mapping will be used to select the image for the platform and the
	// snapshotter for unpacking.
	RuntimePlatforms map[string]ImagePlatform `toml:"runtime_platforms" json:"runtimePlatforms"`

	// Registry contains config related to the registry
	Registry Registry `toml:"registry" json:"registry"`

	// ImageDecryption contains config related to handling decryption of encrypted container images
	ImageDecryption `toml:"image_decryption" json:"imageDecryption"`

	// MaxConcurrentDownloads restricts the number of concurrent downloads for each image.
	// TODO: Migrate to transfer service
	MaxConcurrentDownloads int `toml:"max_concurrent_downloads" json:"maxConcurrentDownloads"`

	// ImagePullProgressTimeout is the maximum duration that there is no
	// image data read from image registry in the open connection. It will
	// be reset whatever a new byte has been read. If timeout, the image
	// pulling will be cancelled. A zero value means there is no timeout.
	//
	// The string is in the golang duration format, see:
	//   https://golang.org/pkg/time/#ParseDuration
	ImagePullProgressTimeout string `toml:"image_pull_progress_timeout" json:"imagePullProgressTimeout"`

	// ImagePullWithSyncFs is an experimental setting. It's to force sync
	// filesystem during unpacking to ensure that data integrity.
	// TODO: Migrate to transfer service
	ImagePullWithSyncFs bool `toml:"image_pull_with_sync_fs" json:"imagePullWithSyncFs"`

	// StatsCollectPeriod is the period (in seconds) of snapshots stats collection.
	StatsCollectPeriod int `toml:"stats_collect_period" json:"statsCollectPeriod"`
}

// PluginConfig contains toml config related to CRI plugin,
// it is a subset of Config.
type PluginConfig struct {
	// ContainerdConfig contains config related to containerd
	ContainerdConfig `toml:"containerd" json:"containerd"`
	// CniConfig contains config related to cni
	CniConfig `toml:"cni" json:"cni"`
	// DisableTCPService disables serving CRI on the TCP server.
	DisableTCPService bool `toml:"disable_tcp_service" json:"disableTCPService"`
	// StreamServerAddress is the ip address streaming server is listening on.
	StreamServerAddress string `toml:"stream_server_address" json:"streamServerAddress"`
	// StreamServerPort is the port streaming server is listening on.
	StreamServerPort string `toml:"stream_server_port" json:"streamServerPort"`
	// StreamIdleTimeout is the maximum time a streaming connection
	// can be idle before the connection is automatically closed.
	// The string is in the golang duration format, see:
	//   https://golang.org/pkg/time/#ParseDuration
	StreamIdleTimeout string `toml:"stream_idle_timeout" json:"streamIdleTimeout"`
	// EnableSelinux indicates to enable the selinux support.
	EnableSelinux bool `toml:"enable_selinux" json:"enableSelinux"`
	// SelinuxCategoryRange allows the upper bound on the category range to be set.
	// If not specified or set to 0, defaults to 1024 from the selinux package.
	SelinuxCategoryRange int `toml:"selinux_category_range" json:"selinuxCategoryRange"`
	// EnableTLSStreaming indicates to enable the TLS streaming support.
	EnableTLSStreaming bool `toml:"enable_tls_streaming" json:"enableTLSStreaming"`
	// X509KeyPairStreaming is a x509 key pair used for TLS streaming
	X509KeyPairStreaming `toml:"x509_key_pair_streaming" json:"x509KeyPairStreaming"`
	// MaxContainerLogLineSize is the maximum log line size in bytes for a container.
	// Log line longer than the limit will be split into multiple lines. Non-positive
	// value means no limit.
	MaxContainerLogLineSize int `toml:"max_container_log_line_size" json:"maxContainerLogSize"`
	// DisableCgroup indicates to disable the cgroup support.
	// This is useful when the containerd does not have permission to access cgroup.
	DisableCgroup bool `toml:"disable_cgroup" json:"disableCgroup"`
	// DisableApparmor indicates to disable the apparmor support.
	// This is useful when the containerd does not have permission to access Apparmor.
	DisableApparmor bool `toml:"disable_apparmor" json:"disableApparmor"`
	// RestrictOOMScoreAdj indicates to limit the lower bound of OOMScoreAdj to the containerd's
	// current OOMScoreADj.
	// This is useful when the containerd does not have permission to decrease OOMScoreAdj.
	RestrictOOMScoreAdj bool `toml:"restrict_oom_score_adj" json:"restrictOOMScoreAdj"`
	// DisableProcMount disables Kubernetes ProcMount support. This MUST be set to `true`
	// when using containerd with Kubernetes <=1.11.
	DisableProcMount bool `toml:"disable_proc_mount" json:"disableProcMount"`
	// UnsetSeccompProfile is the profile containerd/cri will use If the provided seccomp profile is
	// unset (`""`) for a container (default is `unconfined`)
	UnsetSeccompProfile string `toml:"unset_seccomp_profile" json:"unsetSeccompProfile"`
	// TolerateMissingHugetlbController if set to false will error out on create/update
	// container requests with huge page limits if the cgroup controller for hugepages is not present.
	// This helps with supporting Kubernetes <=1.18 out of the box. (default is `true`)
	TolerateMissingHugetlbController bool `toml:"tolerate_missing_hugetlb_controller" json:"tolerateMissingHugetlbController"`
	// DisableHugetlbController indicates to silently disable the hugetlb controller, even when it is
	// present in /sys/fs/cgroup/cgroup.controllers.
	// This helps with running rootless mode + cgroup v2 + systemd but without hugetlb delegation.
	DisableHugetlbController bool `toml:"disable_hugetlb_controller" json:"disableHugetlbController"`
	// DeviceOwnershipFromSecurityContext changes the default behavior of setting container devices uid/gid
	// from CRI's SecurityContext (RunAsUser/RunAsGroup) instead of taking host's uid/gid. Defaults to false.
	DeviceOwnershipFromSecurityContext bool `toml:"device_ownership_from_security_context" json:"device_ownership_from_security_context"`
	// IgnoreImageDefinedVolumes ignores volumes defined by the image. Useful for better resource
	// isolation, security and early detection of issues in the mount configuration when using
	// ReadOnlyRootFilesystem since containers won't silently mount a temporary volume.
	IgnoreImageDefinedVolumes bool `toml:"ignore_image_defined_volumes" json:"ignoreImageDefinedVolumes"`
	// NetNSMountsUnderStateDir places all mounts for network namespaces under StateDir/netns instead
	// of being placed under the hardcoded directory /var/run/netns. Changing this setting requires
	// that all containers are deleted.
	NetNSMountsUnderStateDir bool `toml:"netns_mounts_under_state_dir" json:"netnsMountsUnderStateDir"`
	// EnableUnprivilegedPorts configures net.ipv4.ip_unprivileged_port_start=0
	// for all containers which are not using host network
	// and if it is not overwritten by PodSandboxConfig
	// Note that currently default is set to disabled but target change it in future, see:
	//   https://github.com/kubernetes/kubernetes/issues/102612
	EnableUnprivilegedPorts bool `toml:"enable_unprivileged_ports" json:"enableUnprivilegedPorts"`
	// EnableUnprivilegedICMP configures net.ipv4.ping_group_range="0 2147483647"
	// for all containers which are not using host network, are not running in user namespace
	// and if it is not overwritten by PodSandboxConfig
	// Note that currently default is set to disabled but target change it in future together with EnableUnprivilegedPorts
	EnableUnprivilegedICMP bool `toml:"enable_unprivileged_icmp" json:"enableUnprivilegedICMP"`
	// EnableCDI indicates to enable injection of the Container Device Interface Specifications
	// into the OCI config
	// For more details about CDI and the syntax of CDI Spec files please refer to
	// https://github.com/container-orchestrated-devices/container-device-interface.
	EnableCDI bool `toml:"enable_cdi" json:"enableCDI"`
	// CDISpecDirs is the list of directories to scan for Container Device Interface Specifications
	// For more details about CDI configuration please refer to
	// https://github.com/container-orchestrated-devices/container-device-interface#containerd-configuration
	CDISpecDirs []string `toml:"cdi_spec_dirs" json:"cdiSpecDirs"`

	// DrainExecSyncIOTimeout is the maximum duration to wait for ExecSync
	// API' IO EOF event after exec init process exits. A zero value means
	// there is no timeout.
	//
	// The string is in the golang duration format, see:
	//   https://golang.org/pkg/time/#ParseDuration
	//
	// For example, the value can be '5h', '2h30m', '10s'.
	DrainExecSyncIOTimeout string `toml:"drain_exec_sync_io_timeout" json:"drainExecSyncIOTimeout"`
}

// X509KeyPairStreaming contains the x509 configuration for streaming
type X509KeyPairStreaming struct {
	// TLSCertFile is the path to a certificate file
	TLSCertFile string `toml:"tls_cert_file" json:"tlsCertFile"`
	// TLSKeyFile is the path to a private key file
	TLSKeyFile string `toml:"tls_key_file" json:"tlsKeyFile"`
}

// Config contains all configurations for cri server.
type Config struct {
	// PluginConfig is the config for CRI plugin.
	PluginConfig
	// ContainerdRootDir is the root directory path for containerd.
	ContainerdRootDir string `json:"containerdRootDir"`
	// ContainerdEndpoint is the containerd endpoint path.
	ContainerdEndpoint string `json:"containerdEndpoint"`
	// RootDir is the root directory path for managing cri plugin files
	// (metadata checkpoint etc.)
	RootDir string `json:"rootDir"`
	// StateDir is the root directory path for managing volatile pod/container data
	StateDir string `json:"stateDir"`
}

const (
	// RuntimeUntrusted is the implicit runtime defined for ContainerdConfig.UntrustedWorkloadRuntime
	RuntimeUntrusted = "untrusted"
	// RuntimeDefault is the implicit runtime defined for ContainerdConfig.DefaultRuntime
	RuntimeDefault = "default"
	// KeyModelNode is the key model where key for encrypted images reside
	// on the worker nodes
	KeyModelNode = "node"
)

// ValidateImageConfig validates the given image configuration
func ValidateImageConfig(ctx context.Context, c *ImageConfig) ([]deprecation.Warning, error) {
	var warnings []deprecation.Warning

	useConfigPath := c.Registry.ConfigPath != ""
	if len(c.Registry.Mirrors) > 0 {
		if useConfigPath {
			return warnings, errors.New("`mirrors` cannot be set when `config_path` is provided")
		}
		warnings = append(warnings, deprecation.CRIRegistryMirrors)
		log.G(ctx).Warning("`mirrors` is deprecated, please use `config_path` instead")
	}

	if len(c.Registry.Configs) != 0 {
		warnings = append(warnings, deprecation.CRIRegistryConfigs)
		log.G(ctx).Warning("`configs` is deprecated, please use `config_path` instead")
	}

	// Validation for deprecated auths options and mapping it to configs.
	if len(c.Registry.Auths) != 0 {
		if c.Registry.Configs == nil {
			c.Registry.Configs = make(map[string]RegistryConfig)
		}
		for endpoint, auth := range c.Registry.Auths {
			auth := auth
			u, err := url.Parse(endpoint)
			if err != nil {
				return warnings, fmt.Errorf("failed to parse registry url %q from `registry.auths`: %w", endpoint, err)
			}
			if u.Scheme != "" {
				// Do not include the scheme in the new registry config.
				endpoint = u.Host
			}
			config := c.Registry.Configs[endpoint]
			config.Auth = &auth
			c.Registry.Configs[endpoint] = config
		}
		warnings = append(warnings, deprecation.CRIRegistryAuths)
		log.G(ctx).Warning("`auths` is deprecated, please use `ImagePullSecrets` instead")
	}

	// Validation for image_pull_progress_timeout
	if c.ImagePullProgressTimeout != "" {
		if _, err := time.ParseDuration(c.ImagePullProgressTimeout); err != nil {
			return warnings, fmt.Errorf("invalid image pull progress timeout: %w", err)
		}
	}

	return warnings, nil
}

// ValidatePluginConfig validates the given plugin configuration.
func ValidatePluginConfig(ctx context.Context, c *PluginConfig) ([]deprecation.Warning, error) {
	var warnings []deprecation.Warning
	if c.ContainerdConfig.Runtimes == nil {
		c.ContainerdConfig.Runtimes = make(map[string]Runtime)
	}

	// Validation for default_runtime_name
	if c.ContainerdConfig.DefaultRuntimeName == "" {
		return warnings, errors.New("`default_runtime_name` is empty")
	}
	if _, ok := c.ContainerdConfig.Runtimes[c.ContainerdConfig.DefaultRuntimeName]; !ok {
		return warnings, fmt.Errorf("no corresponding runtime configured in `containerd.runtimes` for `containerd` `default_runtime_name = \"%s\"", c.ContainerdConfig.DefaultRuntimeName)
	}

	for k, r := range c.ContainerdConfig.Runtimes {
		if !r.PrivilegedWithoutHostDevices && r.PrivilegedWithoutHostDevicesAllDevicesAllowed {
			return warnings, errors.New("`privileged_without_host_devices_all_devices_allowed` requires `privileged_without_host_devices` to be enabled")
		}
		// If empty, use default podSandbox mode
		if len(r.Sandboxer) == 0 {
			r.Sandboxer = string(ModePodSandbox)
			c.ContainerdConfig.Runtimes[k] = r
		}
	}

	// Validation for stream_idle_timeout
	if c.StreamIdleTimeout != "" {
		if _, err := time.ParseDuration(c.StreamIdleTimeout); err != nil {
			return warnings, fmt.Errorf("invalid stream idle timeout: %w", err)
		}
	}

	// Validation for drain_exec_sync_io_timeout
	if c.DrainExecSyncIOTimeout != "" {
		if _, err := time.ParseDuration(c.DrainExecSyncIOTimeout); err != nil {
			return warnings, fmt.Errorf("invalid `drain_exec_sync_io_timeout`: %w", err)
		}
	}
	if err := ValidateEnableUnprivileged(ctx, c); err != nil {
		return warnings, err
	}
	return warnings, nil
}

func (config *Config) GetSandboxRuntime(podSandboxConfig *runtime.PodSandboxConfig, runtimeHandler string) (Runtime, error) {
	if untrustedWorkload(podSandboxConfig) {
		// If the untrusted annotation is provided, runtimeHandler MUST be empty.
		if runtimeHandler != "" && runtimeHandler != RuntimeUntrusted {
			return Runtime{}, errors.New("untrusted workload with explicit runtime handler is not allowed")
		}

		//  If the untrusted workload is requesting access to the host/node, this request will fail.
		//
		//  Note: If the workload is marked untrusted but requests privileged, this can be granted, as the
		// runtime may support this.  For example, in a virtual-machine isolated runtime, privileged
		// is a supported option, granting the workload to access the entire guest VM instead of host.
		// TODO(windows): Deprecate this so that we don't need to handle it for windows.
		if hostAccessingSandbox(podSandboxConfig) {
			return Runtime{}, errors.New("untrusted workload with host access is not allowed")
		}

		runtimeHandler = RuntimeUntrusted
	}

	if runtimeHandler == "" {
		runtimeHandler = config.DefaultRuntimeName
	}

	r, ok := config.Runtimes[runtimeHandler]
	if !ok {
		return Runtime{}, fmt.Errorf("no runtime for %q is configured", runtimeHandler)
	}
	return r, nil

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

// GenerateRuntimeOptions generates runtime options from cri plugin config.
func GenerateRuntimeOptions(r Runtime) (interface{}, error) {
	if r.Options == nil {
		return nil, nil
	}

	b, err := toml.Marshal(r.Options)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal TOML blob for runtime %q: %w", r.Type, err)
	}

	options := getRuntimeOptionsType(r.Type)
	if err := toml.Unmarshal(b, options); err != nil {
		return nil, err
	}

	// For generic configuration, if no config path specified (preserving old behavior), pass
	// the whole TOML configuration section to the runtime.
	if runtimeOpts, ok := options.(*runtimeoptions.Options); ok && runtimeOpts.ConfigPath == "" {
		runtimeOpts.ConfigBody = b
	}

	return options, nil
}

// getRuntimeOptionsType gets empty runtime options by the runtime type name.
func getRuntimeOptionsType(t string) interface{} {
	switch t {
	case plugins.RuntimeRuncV2, plugins.RuntimeRuncV2Rs:
		return &runcoptions.Options{}
	case plugins.RuntimeRunhcsV1:
		return &runhcsoptions.Options{}
	default:
		return &runtimeoptions.Options{}
	}
}
