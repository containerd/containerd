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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/containers"
	cri "github.com/containerd/containerd/integration/cri-api/pkg/apis"
	_ "github.com/containerd/containerd/integration/images" // Keep this around to parse `imageListFile` command line var
	"github.com/containerd/containerd/integration/remote"
	dialer "github.com/containerd/containerd/integration/remote/util"
	criconfig "github.com/containerd/containerd/pkg/cri/config"
	"github.com/containerd/containerd/pkg/cri/constants"
	"github.com/containerd/containerd/pkg/cri/server"
	"github.com/containerd/containerd/pkg/cri/util"
	"github.com/opencontainers/selinux/go-selinux"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	exec "golang.org/x/sys/execabs"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

const (
	timeout      = 1 * time.Minute
	k8sNamespace = constants.K8sContainerdNamespace
)

var (
	runtimeService     cri.RuntimeService
	imageService       cri.ImageManagerService
	containerdClient   *containerd.Client
	containerdEndpoint string
)

var criEndpoint = flag.String("cri-endpoint", "unix:///run/containerd/containerd.sock", "The endpoint of cri plugin.")
var criRoot = flag.String("cri-root", "/var/lib/containerd/io.containerd.grpc.v1.cri", "The root directory of cri plugin.")
var runtimeHandler = flag.String("runtime-handler", "", "The runtime handler to use in the test.")
var containerdBin = flag.String("containerd-bin", "containerd", "The containerd binary name. The name is used to restart containerd during test.")

func TestMain(m *testing.M) {
	flag.Parse()
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
		return fmt.Errorf("failed to create runtime service: %w", err)
	}
	imageService, err = remote.NewImageService(*criEndpoint, timeout)
	if err != nil {
		return fmt.Errorf("failed to create image service: %w", err)
	}
	// Since CRI grpc client doesn't have `WithBlock` specified, we
	// need to check whether it is actually connected.
	// TODO(#6069) Use grpc options to block on connect and remove for this list containers request.
	_, err = runtimeService.ListContainers(&runtime.ContainerFilter{})
	if err != nil {
		return fmt.Errorf("failed to list containers: %w", err)
	}
	_, err = imageService.ListImages(&runtime.ImageFilter{})
	if err != nil {
		return fmt.Errorf("failed to list images: %w", err)
	}
	// containerdEndpoint is the same with criEndpoint now
	containerdEndpoint = strings.TrimPrefix(*criEndpoint, "unix://")
	containerdEndpoint = strings.TrimPrefix(containerdEndpoint, "npipe:")
	containerdClient, err = containerd.New(containerdEndpoint, containerd.WithDefaultNamespace(k8sNamespace))
	if err != nil {
		return fmt.Errorf("failed to connect containerd: %w", err)
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

// Set pod userns.
func WithPodUserNs(containerID, hostID, length uint32) PodSandboxOpts {
	return func(p *runtime.PodSandboxConfig) {
		if p.Linux == nil {
			p.Linux = &runtime.LinuxPodSandboxConfig{}
		}
		if p.Linux.SecurityContext == nil {
			p.Linux.SecurityContext = &runtime.LinuxSandboxSecurityContext{}
		}
		if p.Linux.SecurityContext.NamespaceOptions == nil {
			p.Linux.SecurityContext.NamespaceOptions = &runtime.NamespaceOption{}
		}

		idMap := runtime.IDMapping{
			HostId:      hostID,
			ContainerId: containerID,
			Length:      length,
		}
		if p.Linux.SecurityContext.NamespaceOptions.UsernsOptions == nil {
			p.Linux.SecurityContext.NamespaceOptions.UsernsOptions = &runtime.UserNamespace{
				Mode: runtime.NamespaceMode_POD,
			}
		}

		p.Linux.SecurityContext.NamespaceOptions.UsernsOptions.Uids = append(p.Linux.SecurityContext.NamespaceOptions.UsernsOptions.Uids, &idMap)
		p.Linux.SecurityContext.NamespaceOptions.UsernsOptions.Gids = append(p.Linux.SecurityContext.NamespaceOptions.UsernsOptions.Gids, &idMap)
	}
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

// Add pod labels.
func WithPodLabels(kvs map[string]string) PodSandboxOpts {
	return func(p *runtime.PodSandboxConfig) {
		for k, v := range kvs {
			p.Labels[k] = v
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
		Linux:       &runtime.LinuxPodSandboxConfig{},
		Annotations: make(map[string]string),
		Labels:      make(map[string]string),
	}
	for _, opt := range opts {
		opt(config)
	}
	return config
}

func PodSandboxConfigWithCleanup(t *testing.T, name, ns string, opts ...PodSandboxOpts) (string, *runtime.PodSandboxConfig) {
	sbConfig := PodSandboxConfig(name, ns, opts...)
	sb, err := runtimeService.RunPodSandbox(sbConfig, *runtimeHandler)
	require.NoError(t, err)
	t.Cleanup(func() {
		assert.NoError(t, runtimeService.StopPodSandbox(sb))
		assert.NoError(t, runtimeService.RemovePodSandbox(sb))
	})

	return sb, sbConfig
}

// Set Windows HostProcess on the pod.
func WithWindowsHostProcessPod(p *runtime.PodSandboxConfig) {
	if p.Windows == nil {
		p.Windows = &runtime.WindowsPodSandboxConfig{}
	}
	if p.Windows.SecurityContext == nil {
		p.Windows.SecurityContext = &runtime.WindowsSandboxSecurityContext{}
	}
	p.Windows.SecurityContext.HostProcess = true
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

// Adds Windows container resource limits.
func WithWindowsResources(r *runtime.WindowsContainerResources) ContainerOpts {
	return func(c *runtime.ContainerConfig) {
		if c.Windows == nil {
			c.Windows = &runtime.WindowsContainerConfig{}
		}
		c.Windows.Resources = r
	}
}

func WithVolumeMount(hostPath, containerPath string) ContainerOpts {
	return func(c *runtime.ContainerConfig) {
		hostPath, _ = filepath.Abs(hostPath)
		containerPath, _ = filepath.Abs(containerPath)
		mount := &runtime.Mount{
			HostPath:       hostPath,
			ContainerPath:  containerPath,
			SelinuxRelabel: selinux.GetEnabled(),
		}
		c.Mounts = append(c.Mounts, mount)
	}
}

func WithWindowsUsername(username string) ContainerOpts {
	return func(c *runtime.ContainerConfig) {
		if c.Windows == nil {
			c.Windows = &runtime.WindowsContainerConfig{}
		}
		if c.Windows.SecurityContext == nil {
			c.Windows.SecurityContext = &runtime.WindowsContainerSecurityContext{}
		}
		c.Windows.SecurityContext.RunAsUsername = username
	}
}

func WithWindowsHostProcessContainer() ContainerOpts {
	return func(c *runtime.ContainerConfig) {
		if c.Windows == nil {
			c.Windows = &runtime.WindowsContainerConfig{}
		}
		if c.Windows.SecurityContext == nil {
			c.Windows.SecurityContext = &runtime.WindowsContainerSecurityContext{}
		}
		c.Windows.SecurityContext.HostProcess = true
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

// Add user namespace pod mode.
func WithUserNamespace(containerID, hostID, length uint32) ContainerOpts {
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
		idMap := runtime.IDMapping{
			HostId:      hostID,
			ContainerId: containerID,
			Length:      length,
		}

		if c.Linux.SecurityContext.NamespaceOptions.UsernsOptions == nil {
			c.Linux.SecurityContext.NamespaceOptions.UsernsOptions = &runtime.UserNamespace{
				Mode: runtime.NamespaceMode_POD,
			}
		}

		c.Linux.SecurityContext.NamespaceOptions.UsernsOptions.Uids = append(c.Linux.SecurityContext.NamespaceOptions.UsernsOptions.Uids, &idMap)
		c.Linux.SecurityContext.NamespaceOptions.UsernsOptions.Gids = append(c.Linux.SecurityContext.NamespaceOptions.UsernsOptions.Gids, &idMap)
	}
}

// Add container log path.
func WithLogPath(path string) ContainerOpts {
	return func(c *runtime.ContainerConfig) {
		c.LogPath = path
	}
}

// WithRunAsUser sets the uid.
func WithRunAsUser(uid int64) ContainerOpts {
	return func(c *runtime.ContainerConfig) {
		if c.Linux == nil {
			c.Linux = &runtime.LinuxContainerConfig{}
		}
		if c.Linux.SecurityContext == nil {
			c.Linux.SecurityContext = &runtime.LinuxContainerSecurityContext{}
		}
		c.Linux.SecurityContext.RunAsUser = &runtime.Int64Value{Value: uid}
	}
}

// WithRunAsUsername sets the username.
func WithRunAsUsername(username string) ContainerOpts {
	return func(c *runtime.ContainerConfig) {
		if c.Linux == nil {
			c.Linux = &runtime.LinuxContainerConfig{}
		}
		if c.Linux.SecurityContext == nil {
			c.Linux.SecurityContext = &runtime.LinuxContainerSecurityContext{}
		}
		c.Linux.SecurityContext.RunAsUsername = username
	}
}

// WithRunAsGroup sets the gid.
func WithRunAsGroup(gid int64) ContainerOpts {
	return func(c *runtime.ContainerConfig) {
		if c.Linux == nil {
			c.Linux = &runtime.LinuxContainerConfig{}
		}
		if c.Linux.SecurityContext == nil {
			c.Linux.SecurityContext = &runtime.LinuxContainerSecurityContext{}
		}
		c.Linux.SecurityContext.RunAsGroup = &runtime.Int64Value{Value: gid}
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

// WithDevice adds a device mount.
func WithDevice(containerPath, hostPath, permissions string) ContainerOpts {
	return func(c *runtime.ContainerConfig) {
		c.Devices = append(c.Devices, &runtime.Device{
			ContainerPath: containerPath, HostPath: hostPath, Permissions: permissions,
		})
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
func KillProcess(name string, signal syscall.Signal) error {
	var command []string
	if goruntime.GOOS == "windows" {
		command = []string{"taskkill", "/IM", name, "/F"}
	} else {
		command = []string{"pkill", "-" + strconv.Itoa(int(signal)), "-x", fmt.Sprintf("^%s$", name)}
	}

	output, err := exec.Command(command[0], command[1:]...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to kill %q - error: %v, output: %q", name, err, output)
	}
	return nil
}

// KillPid kills the process by pid. kill is used.
func KillPid(pid int) error {
	command := "kill"
	if goruntime.GOOS == "windows" {
		command = "tskill"
	}
	output, err := exec.Command(command, strconv.Itoa(pid)).CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to kill %d - error: %v, output: %q", pid, err, output)
	}
	return nil
}

// PidOf returns pid of a process by name.
func PidOf(name string) (int, error) {
	b, err := exec.Command("pidof", "-s", name).CombinedOutput()
	output := strings.TrimSpace(string(b))
	if err != nil {
		if len(output) != 0 {
			return 0, fmt.Errorf("failed to run pidof %q - error: %v, output: %q", name, err, output)
		}
		return 0, nil
	}
	return strconv.Atoi(output)
}

// PidsOf returns pid(s) of a process by name
func PidsOf(name string) ([]int, error) {
	if len(name) == 0 {
		return []int{}, fmt.Errorf("name is required")
	}

	procDirFD, err := os.Open("/proc")
	if err != nil {
		return nil, fmt.Errorf("failed to open /proc: %w", err)
	}
	defer procDirFD.Close()

	res := []int{}
	for {
		fileInfos, err := procDirFD.Readdir(100)
		if err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("failed to readdir: %w", err)
		}

		for _, fileInfo := range fileInfos {
			if !fileInfo.IsDir() {
				continue
			}

			pid, err := strconv.Atoi(fileInfo.Name())
			if err != nil {
				continue
			}

			exePath, err := os.Readlink(filepath.Join("/proc", fileInfo.Name(), "exe"))
			if err != nil {
				continue
			}

			if strings.HasSuffix(exePath, name) {
				res = append(res, pid)
			}
		}
	}
	return res, nil
}

// PidEnvs returns the environ of pid in key-value pairs.
func PidEnvs(pid int) (map[string]string, error) {
	envPath := filepath.Join("/proc", strconv.Itoa(pid), "environ")

	b, err := os.ReadFile(envPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", envPath, err)
	}

	values := bytes.Split(b, []byte{0})
	if len(values) == 0 {
		return nil, nil
	}

	res := make(map[string]string)
	for _, value := range values {
		value = bytes.TrimSpace(value)
		if len(value) == 0 {
			continue
		}

		k, v, ok := strings.Cut(string(value), "=")
		if ok {
			res[k] = v
		}
	}
	return res, nil
}

// RawRuntimeClient returns a raw grpc runtime service client.
func RawRuntimeClient() (runtime.RuntimeServiceClient, error) {
	addr, dialer, err := dialer.GetAddressAndDialer(*criEndpoint)
	if err != nil {
		return nil, fmt.Errorf("failed to get dialer: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	conn, err := grpc.DialContext(ctx, addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(dialer),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to connect cri endpoint: %w", err)
	}
	return runtime.NewRuntimeServiceClient(conn), nil
}

// CRIConfig gets current cri config from containerd.
func CRIConfig() (*criconfig.Config, error) {
	client, err := RawRuntimeClient()
	if err != nil {
		return nil, fmt.Errorf("failed to get raw runtime client: %w", err)
	}
	resp, err := client.Status(context.Background(), &runtime.StatusRequest{Verbose: true})
	if err != nil {
		return nil, fmt.Errorf("failed to get status: %w", err)
	}
	config := &criconfig.Config{}
	if err := json.Unmarshal([]byte(resp.Info["config"]), config); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}
	return config, nil
}

// SandboxInfo gets sandbox info.
func SandboxInfo(id string) (*runtime.PodSandboxStatus, *server.SandboxInfo, error) {
	client, err := RawRuntimeClient()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get raw runtime client: %w", err)
	}
	resp, err := client.PodSandboxStatus(context.Background(), &runtime.PodSandboxStatusRequest{
		PodSandboxId: id,
		Verbose:      true,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get sandbox status: %w", err)
	}
	status := resp.GetStatus()
	var info server.SandboxInfo
	if err := json.Unmarshal([]byte(resp.GetInfo()["info"]), &info); err != nil {
		return nil, nil, fmt.Errorf("failed to unmarshal sandbox info: %w", err)
	}
	return status, &info, nil
}

func RestartContainerd(t *testing.T, signal syscall.Signal) {
	require.NoError(t, KillProcess(*containerdBin, signal))

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

// EnsureImageExists pulls the given image, ensures that no error was encountered
// while pulling it.
func EnsureImageExists(t *testing.T, imageName string) string {
	img, err := imageService.ImageStatus(&runtime.ImageSpec{Image: imageName})
	require.NoError(t, err)
	if img != nil {
		t.Logf("Image %q already exists, not pulling.", imageName)
		return img.Id
	}

	t.Logf("Pull test image %q", imageName)
	imgID, err := imageService.PullImage(&runtime.ImageSpec{Image: imageName}, nil, nil)
	require.NoError(t, err)

	return imgID
}

func GetContainer(id string) (containers.Container, error) {
	return containerdClient.ContainerService().Get(context.Background(), id)
}
