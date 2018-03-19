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
	"flag"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/containerd/containerd"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"k8s.io/kubernetes/pkg/kubelet/apis/cri"
	runtime "k8s.io/kubernetes/pkg/kubelet/apis/cri/runtime/v1alpha2"
	"k8s.io/kubernetes/pkg/kubelet/remote"

	api "github.com/containerd/cri/pkg/api/v1"
	"github.com/containerd/cri/pkg/client"
	"github.com/containerd/cri/pkg/constants"
	"github.com/containerd/cri/pkg/util"
)

const (
	timeout            = 1 * time.Minute
	pauseImage         = "gcr.io/google_containers/pause:3.0" // This is the same with default sandbox image.
	k8sNamespace       = constants.K8sContainerdNamespace
	containerdEndpoint = "/run/containerd/containerd.sock"
)

var (
	runtimeService   cri.RuntimeService
	imageService     cri.ImageManagerService
	containerdClient *containerd.Client
	criPluginClient  api.CRIPluginServiceClient
)

var criEndpoint = flag.String("cri-endpoint", "/run/containerd/containerd.sock", "The endpoint of cri plugin.")
var criRoot = flag.String("cri-root", "/var/lib/containerd/io.containerd.grpc.v1.cri", "The root directory of cri plugin.")

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
	criPluginClient, err = client.NewCRIPluginClient(*criEndpoint, timeout)
	if err != nil {
		return errors.Wrap(err, "failed to connect cri plugin")
	}
	return nil
}

// Opts sets specific information in pod sandbox config.
type PodSandboxOpts func(*runtime.PodSandboxConfig)

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
	return func(cf *runtime.ContainerConfig) {
		cf.Labels = map[string]string{"key": "value"}
	}
}

func WithTestAnnotations() ContainerOpts {
	return func(cf *runtime.ContainerConfig) {
		cf.Annotations = map[string]string{"a.b.c": "test"}
	}
}

// Add container resource limits.
func WithResources(r *runtime.LinuxContainerResources) ContainerOpts {
	return func(cf *runtime.ContainerConfig) {
		if cf.Linux == nil {
			cf.Linux = &runtime.LinuxContainerConfig{}
		}
		cf.Linux.Resources = r
	}
}

// Add container command.
func WithCommand(c string, args ...string) ContainerOpts {
	return func(cf *runtime.ContainerConfig) {
		cf.Command = []string{c}
		cf.Args = args
	}
}

// Add pid namespace mode.
func WithPidNamespace(mode runtime.NamespaceMode) ContainerOpts {
	return func(cf *runtime.ContainerConfig) {
		if cf.Linux == nil {
			cf.Linux = &runtime.LinuxContainerConfig{}
		}
		if cf.Linux.SecurityContext == nil {
			cf.Linux.SecurityContext = &runtime.LinuxContainerSecurityContext{}
		}
		if cf.Linux.SecurityContext.NamespaceOptions == nil {
			cf.Linux.SecurityContext.NamespaceOptions = &runtime.NamespaceOption{}
		}
		cf.Linux.SecurityContext.NamespaceOptions.Pid = mode
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
