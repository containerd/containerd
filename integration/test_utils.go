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

package integration

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/containerd/containerd"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	"k8s.io/kubernetes/pkg/kubelet/apis/cri"
	runtime "k8s.io/kubernetes/pkg/kubelet/apis/cri/runtime/v1alpha2"
	"k8s.io/kubernetes/pkg/kubelet/remote"
	kubeletutil "k8s.io/kubernetes/pkg/kubelet/util"

	api "github.com/containerd/cri/pkg/api/v1"
	"github.com/containerd/cri/pkg/client"
	criconfig "github.com/containerd/cri/pkg/config"
	"github.com/containerd/cri/pkg/constants"
	"github.com/containerd/cri/pkg/util"
)

const (
	timeout            = 1 * time.Minute
	pauseImage         = "k8s.gcr.io/pause:3.1" // This is the same with default sandbox image.
	k8sNamespace       = constants.K8sContainerdNamespace
	containerdEndpoint = "/run/containerd/containerd.sock"
)

var (
	runtimeService   cri.RuntimeService
	imageService     cri.ImageManagerService
	containerdClient *containerd.Client
	criPluginClient  api.CRIPluginServiceClient
)

var criEndpoint = flag.String("cri-endpoint", "unix:///run/containerd/containerd.sock", "The endpoint of cri plugin.")
var criRoot = flag.String("cri-root", "/var/lib/containerd/io.containerd.grpc.v1.cri", "The root directory of cri plugin.")
var runtimeHandler = flag.String("runtime-handler", "", "The runtime handler to use in the test.")

func init() {
	flag.Parse()
	if err := ConnectDaemons(); err != nil {
		logrus.WithError(err).Fatalf("Failed to connect daemons")
	}
}

// ConnectDaemons connect cri plugin and containerd, and initialize the clients.
func ConnectDaemons() error {
	var err error
	runtimeService, err = remote.NewRemoteRuntimeService(*criEndpoint, timeout)
	if err != nil {
		return errors.Wrap(err, "failed to create runtime service")
	}
	imageService, err = remote.NewRemoteImageService(*criEndpoint, timeout)
	if err != nil {
		return errors.Wrap(err, "failed to create image service")
	}
	// Since CRI grpc client doesn't have `WithBlock` specified, we
	// need to check whether it is actually connected.
	// TODO(random-liu): Extend cri remote client to accept extra grpc options.
	_, err = runtimeService.ListContainers(&runtime.ContainerFilter{})
	if err != nil {
		return errors.Wrap(err, "failed to list containers")
	}
	_, err = imageService.ListImages(&runtime.ImageFilter{})
	if err != nil {
		return errors.Wrap(err, "failed to list images")
	}
	containerdClient, err = containerd.New(containerdEndpoint, containerd.WithDefaultNamespace(k8sNamespace))
	if err != nil {
		return errors.Wrap(err, "failed to connect containerd")
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	criPluginClient, err = client.NewCRIPluginClient(ctx, *criEndpoint)
	if err != nil {
		return errors.Wrap(err, "failed to connect cri plugin")
	}
	return nil
}

// Opts sets specific information in pod sandbox config.
type PodSandboxOpts func(*runtime.PodSandboxConfig)

// Set host network.
func WithHostNetwork(p *runtime.PodSandboxConfig) {
	if p.Linux == nil {
		p.Linux = &runtime.LinuxPodSandboxConfig{}
	}
	if p.Linux.SecurityContext == nil {
		p.Linux.SecurityContext = &runtime.LinuxSandboxSecurityContext{}
	}
	if p.Linux.SecurityContext.NamespaceOptions == nil {
		p.Linux.SecurityContext.NamespaceOptions = &runtime.NamespaceOption{
			Network: runtime.NamespaceMode_NODE,
		}
	}
}

// Add pod log directory.
func WithPodLogDirectory(dir string) PodSandboxOpts {
	return func(p *runtime.PodSandboxConfig) {
		p.LogDirectory = dir
	}
}

// Add pod hostname.
func WithPodHostname(hostname string) PodSandboxOpts {
	return func(p *runtime.PodSandboxConfig) {
		p.Hostname = hostname
	}
}

// PodSandboxConfig generates a pod sandbox config for test.
func PodSandboxConfig(name, ns string, opts ...PodSandboxOpts) *runtime.PodSandboxConfig {
	config := &runtime.PodSandboxConfig{
		Metadata: &runtime.PodSandboxMetadata{
			Name: name,
			// Using random id as uuid is good enough for local
			// integration test.
			Uid:       util.GenerateID(),
			Namespace: Randomize(ns),
		},
		Linux: &runtime.LinuxPodSandboxConfig{},
	}
	for _, opt := range opts {
		opt(config)
	}
	return config
}

// ContainerOpts to set any specific attribute like labels,
// annotations, metadata etc
type ContainerOpts func(*runtime.ContainerConfig)

func WithTestLabels() ContainerOpts {
	return func(c *runtime.ContainerConfig) {
		c.Labels = map[string]string{"key": "value"}
	}
}

func WithTestAnnotations() ContainerOpts {
	return func(c *runtime.ContainerConfig) {
		c.Annotations = map[string]string{"a.b.c": "test"}
	}
}

// Add container resource limits.
func WithResources(r *runtime.LinuxContainerResources) ContainerOpts {
	return func(c *runtime.ContainerConfig) {
		if c.Linux == nil {
			c.Linux = &runtime.LinuxContainerConfig{}
		}
		c.Linux.Resources = r
	}
}

// Add container command.
func WithCommand(cmd string, args ...string) ContainerOpts {
	return func(c *runtime.ContainerConfig) {
		c.Command = []string{cmd}
		c.Args = args
	}
}

// Add pid namespace mode.
func WithPidNamespace(mode runtime.NamespaceMode) ContainerOpts {
	return func(c *runtime.ContainerConfig) {
		if c.Linux == nil {
			c.Linux = &runtime.LinuxContainerConfig{}
		}
		if c.Linux.SecurityContext == nil {
			c.Linux.SecurityContext = &runtime.LinuxContainerSecurityContext{}
		}
		if c.Linux.SecurityContext.NamespaceOptions == nil {
			c.Linux.SecurityContext.NamespaceOptions = &runtime.NamespaceOption{}
		}
		c.Linux.SecurityContext.NamespaceOptions.Pid = mode
	}

}

// Add container log path.
func WithLogPath(path string) ContainerOpts {
	return func(c *runtime.ContainerConfig) {
		c.LogPath = path
	}
}

// WithSupplementalGroups adds supplemental groups.
func WithSupplementalGroups(gids []int64) ContainerOpts {
	return func(c *runtime.ContainerConfig) {
		if c.Linux == nil {
			c.Linux = &runtime.LinuxContainerConfig{}
		}
		if c.Linux.SecurityContext == nil {
			c.Linux.SecurityContext = &runtime.LinuxContainerSecurityContext{}
		}
		c.Linux.SecurityContext.SupplementalGroups = gids
	}
}

// ContainerConfig creates a container config given a name and image name
// and additional container config options
func ContainerConfig(name, image string, opts ...ContainerOpts) *runtime.ContainerConfig {
	cConfig := &runtime.ContainerConfig{
		Metadata: &runtime.ContainerMetadata{
			Name: name,
		},
		Image: &runtime.ImageSpec{Image: image},
	}
	for _, opt := range opts {
		opt(cConfig)
	}
	return cConfig
}

// CheckFunc is the function used to check a condition is true/false.
type CheckFunc func() (bool, error)

// Eventually waits for f to return true, it checks every period, and
// returns error if timeout exceeds. If f returns error, Eventually
// will return the same error immediately.
func Eventually(f CheckFunc, period, timeout time.Duration) error {
	start := time.Now()
	for {
		done, err := f()
		if done {
			return nil
		}
		if err != nil {
			return err
		}
		if time.Since(start) >= timeout {
			return errors.New("timeout exceeded")
		}
		time.Sleep(period)
	}
}

// Consistently makes sure that f consistently returns true without
// error before timeout exceeds. If f returns error, Consistently
// will return the same error immediately.
func Consistently(f CheckFunc, period, timeout time.Duration) error {
	start := time.Now()
	for {
		ok, err := f()
		if !ok {
			return errors.New("get false")
		}
		if err != nil {
			return err
		}
		if time.Since(start) >= timeout {
			return nil
		}
		time.Sleep(period)
	}
}

// Randomize adds uuid after a string.
func Randomize(str string) string {
	return str + "-" + util.GenerateID()
}

// KillProcess kills the process by name. pkill is used.
func KillProcess(name string) error {
	output, err := exec.Command("pkill", "-x", fmt.Sprintf("^%s$", name)).CombinedOutput()
	if err != nil {
		return errors.Errorf("failed to kill %q - error: %v, output: %q", name, err, output)
	}
	return nil
}

// PidOf returns pid of a process by name.
func PidOf(name string) (int, error) {
	b, err := exec.Command("pidof", name).CombinedOutput()
	output := strings.TrimSpace(string(b))
	if err != nil {
		if len(output) != 0 {
			return 0, errors.Errorf("failed to run pidof %q - error: %v, output: %q", name, err, output)
		}
		return 0, nil
	}
	return strconv.Atoi(output)
}

// CRIConfig gets current cri config from containerd.
func CRIConfig() (*criconfig.Config, error) {
	addr, dialer, err := kubeletutil.GetAddressAndDialer(*criEndpoint)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get dialer")
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	conn, err := grpc.DialContext(ctx, addr, grpc.WithInsecure(), grpc.WithDialer(dialer))
	if err != nil {
		return nil, errors.Wrap(err, "failed to connect cri endpoint")
	}
	client := runtime.NewRuntimeServiceClient(conn)
	resp, err := client.Status(ctx, &runtime.StatusRequest{Verbose: true})
	if err != nil {
		return nil, errors.Wrap(err, "failed to get status")
	}
	config := &criconfig.Config{}
	if err := json.Unmarshal([]byte(resp.Info["config"]), config); err != nil {
		return nil, errors.Wrap(err, "failed to unmarshal config")
	}
	return config, nil
}
