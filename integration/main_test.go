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

package integration

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/containerd/containerd"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	cri "k8s.io/cri-api/pkg/apis"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1alpha2"

	"github.com/containerd/containerd/integration/remote"
	dialer "github.com/containerd/containerd/integration/util"
	criconfig "github.com/containerd/containerd/pkg/cri/config"
	"github.com/containerd/containerd/pkg/cri/constants"
	"github.com/containerd/containerd/pkg/cri/server"
	"github.com/containerd/containerd/pkg/cri/util"
)

const (
	timeout      = 1 * time.Minute
	k8sNamespace = constants.K8sContainerdNamespace
)

var (
	runtimeService     cri.RuntimeService
	containerdClient   *containerd.Client
	containerdEndpoint string
)

var criEndpoint = flag.String("cri-endpoint", "unix:///run/containerd/containerd.sock", "The endpoint of cri plugin.")
var criRoot = flag.String("cri-root", "/var/lib/containerd/io.containerd.grpc.v1.cri", "The root directory of cri plugin.")
var runtimeHandler = flag.String("runtime-handler", "", "The runtime handler to use in the test.")
var containerdBin = flag.String("containerd-bin", "containerd", "The containerd binary name. The name is used to restart containerd during test.")
var imageListFile = flag.String("image-list", "", "The TOML file containing the non-default images to be used in tests.")

func TestMain(m *testing.M) {
	flag.Parse()
	initImages(*imageListFile)
	if err := ConnectDaemons(); err != nil {
		logrus.WithError(err).Fatalf("Failed to connect daemons")
	}
	os.Exit(m.Run())
}

// ConnectDaemons connect cri plugin and containerd, and initialize the clients.
func ConnectDaemons() error {
	var err error
	runtimeService, err = remote.NewRuntimeService(*criEndpoint, timeout)
	if err != nil {
		return errors.Wrap(err, "failed to create runtime service")
	}
	imageService, err = remote.NewImageService(*criEndpoint, timeout)
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
	// containerdEndpoint is the same with criEndpoint now
	containerdEndpoint = strings.TrimPrefix(*criEndpoint, "unix://")
	containerdEndpoint = strings.TrimPrefix(containerdEndpoint, "npipe:")
	containerdClient, err = containerd.New(containerdEndpoint, containerd.WithDefaultNamespace(k8sNamespace))
	if err != nil {
		return errors.Wrap(err, "failed to connect containerd")
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
		p.Linux.SecurityContext.NamespaceOptions = &runtime.NamespaceOption{}
	}
	p.Linux.SecurityContext.NamespaceOptions.Network = runtime.NamespaceMode_NODE
}

// Set host pid.
func WithHostPid(p *runtime.PodSandboxConfig) {
	if p.Linux == nil {
		p.Linux = &runtime.LinuxPodSandboxConfig{}
	}
	if p.Linux.SecurityContext == nil {
		p.Linux.SecurityContext = &runtime.LinuxSandboxSecurityContext{}
	}
	if p.Linux.SecurityContext.NamespaceOptions == nil {
		p.Linux.SecurityContext.NamespaceOptions = &runtime.NamespaceOption{}
	}
	p.Linux.SecurityContext.NamespaceOptions.Pid = runtime.NamespaceMode_NODE
}

// Set pod pid.
func WithPodPid(p *runtime.PodSandboxConfig) {
	if p.Linux == nil {
		p.Linux = &runtime.LinuxPodSandboxConfig{}
	}
	if p.Linux.SecurityContext == nil {
		p.Linux.SecurityContext = &runtime.LinuxSandboxSecurityContext{}
	}
	if p.Linux.SecurityContext.NamespaceOptions == nil {
		p.Linux.SecurityContext.NamespaceOptions = &runtime.NamespaceOption{}
	}
	p.Linux.SecurityContext.NamespaceOptions.Pid = runtime.NamespaceMode_POD
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
func WithResources(r *runtime.LinuxContainerResources) ContainerOpts { //nolint:unused
	return func(c *runtime.ContainerConfig) {
		if c.Linux == nil {
			c.Linux = &runtime.LinuxContainerConfig{}
		}
		c.Linux.Resources = r
	}
}

func WithVolumeMount(hostPath, containerPath string) ContainerOpts {
	return func(c *runtime.ContainerConfig) {
		hostPath, _ = filepath.Abs(hostPath)
		containerPath, _ = filepath.Abs(containerPath)
		mount := &runtime.Mount{HostPath: hostPath, ContainerPath: containerPath}
		c.Mounts = append(c.Mounts, mount)
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
func WithSupplementalGroups(gids []int64) ContainerOpts { //nolint:unused
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

// KillPid kills the process by pid. kill is used.
func KillPid(pid int) error { //nolint:unused
	output, err := exec.Command("kill", strconv.Itoa(pid)).CombinedOutput()
	if err != nil {
		return errors.Errorf("failed to kill %d - error: %v, output: %q", pid, err, output)
	}
	return nil
}

// PidOf returns pid of a process by name.
func PidOf(name string) (int, error) {
	b, err := exec.Command("pidof", "-s", name).CombinedOutput()
	output := strings.TrimSpace(string(b))
	if err != nil {
		if len(output) != 0 {
			return 0, errors.Errorf("failed to run pidof %q - error: %v, output: %q", name, err, output)
		}
		return 0, nil
	}
	return strconv.Atoi(output)
}

// RawRuntimeClient returns a raw grpc runtime service client.
func RawRuntimeClient() (runtime.RuntimeServiceClient, error) {
	addr, dialer, err := dialer.GetAddressAndDialer(*criEndpoint)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get dialer")
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	conn, err := grpc.DialContext(ctx, addr, grpc.WithInsecure(), grpc.WithContextDialer(dialer))
	if err != nil {
		return nil, errors.Wrap(err, "failed to connect cri endpoint")
	}
	return runtime.NewRuntimeServiceClient(conn), nil
}

// CRIConfig gets current cri config from containerd.
func CRIConfig() (*criconfig.Config, error) {
	client, err := RawRuntimeClient()
	if err != nil {
		return nil, errors.Wrap(err, "failed to get raw runtime client")
	}
	resp, err := client.Status(context.Background(), &runtime.StatusRequest{Verbose: true})
	if err != nil {
		return nil, errors.Wrap(err, "failed to get status")
	}
	config := &criconfig.Config{}
	if err := json.Unmarshal([]byte(resp.Info["config"]), config); err != nil {
		return nil, errors.Wrap(err, "failed to unmarshal config")
	}
	return config, nil
}

// SandboxInfo gets sandbox info.
func SandboxInfo(id string) (*runtime.PodSandboxStatus, *server.SandboxInfo, error) { //nolint:unused
	client, err := RawRuntimeClient()
	if err != nil {
		return nil, nil, errors.Wrap(err, "failed to get raw runtime client")
	}
	resp, err := client.PodSandboxStatus(context.Background(), &runtime.PodSandboxStatusRequest{
		PodSandboxId: id,
		Verbose:      true,
	})
	if err != nil {
		return nil, nil, errors.Wrap(err, "failed to get sandbox status")
	}
	status := resp.GetStatus()
	var info server.SandboxInfo
	if err := json.Unmarshal([]byte(resp.GetInfo()["info"]), &info); err != nil {
		return nil, nil, errors.Wrap(err, "failed to unmarshal sandbox info")
	}
	return status, &info, nil
}

func RestartContainerd(t *testing.T) {
	require.NoError(t, KillProcess(*containerdBin))

	// Use assert so that the 3rd wait always runs, this makes sure
	// containerd is running before this function returns.
	assert.NoError(t, Eventually(func() (bool, error) {
		pid, err := PidOf(*containerdBin)
		if err != nil {
			return false, err
		}
		return pid == 0, nil
	}, time.Second, 30*time.Second), "wait for containerd to be killed")

	require.NoError(t, Eventually(func() (bool, error) {
		return ConnectDaemons() == nil, nil
	}, time.Second, 30*time.Second), "wait for containerd to be restarted")
}
