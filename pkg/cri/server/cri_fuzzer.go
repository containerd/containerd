//go:build gofuzz
// +build gofuzz

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
	"fmt"
	golangruntime "runtime"
	"strings"
	"sync"
	"time"

	fuzz "github.com/AdaLogics/go-fuzz-headers"
	"github.com/containerd/go-cni"

	criconfig "github.com/containerd/containerd/pkg/cri/config"
	servertesting "github.com/containerd/containerd/pkg/cri/server/testing"
	containerstore "github.com/containerd/containerd/pkg/cri/store/container"
	imagestore "github.com/containerd/containerd/pkg/cri/store/image"
	"github.com/containerd/containerd/pkg/cri/store/label"
	sandboxstore "github.com/containerd/containerd/pkg/cri/store/sandbox"
	snapshotstore "github.com/containerd/containerd/pkg/cri/store/snapshot"
	ostesting "github.com/containerd/containerd/pkg/os/testing"
	"github.com/containerd/containerd/pkg/registrar"

	"github.com/containerd/containerd"
	_ "github.com/containerd/containerd/cmd/containerd/builtins"
	"github.com/containerd/containerd/cmd/containerd/command"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

var (
	// The APIs the fuzzer can call:
	ops = map[int]string{
		0:  "createContainer",
		1:  "removeContainer",
		2:  "addSandboxes",
		3:  "listContainers",
		4:  "startContainer",
		5:  "containerStats",
		6:  "listContainerStats",
		7:  "containerStatus",
		8:  "stopContainer",
		9:  "updateContainerResources",
		10: "listImages",
		11: "removeImages",
		12: "imageStatus",
		13: "imageFsInfo",
		14: "listPodSandbox",
		15: "portForward",
		16: "removePodSandbox",
		17: "runPodSandbox",
		18: "podSandboxStatus",
		19: "stopPodSandbox",
		20: "status",
		21: "updateRuntimeConfig",
	}
)

const (
	defaultRoot    = "/var/lib/containerd"
	defaultState   = "/tmp/containerd"
	defaultAddress = "/tmp/containerd/containerd.sock"
)

var (
	initDaemon sync.Once

	executionOrder []string
)

func startDaemonForFuzzing(arguments []string) {
	app := command.App()
	_ = app.Run(arguments)
}

func startDaemon() {
	args := []string{"--log-level", "debug"}
	go func() {
		// This is similar to invoking the
		// containerd binary.
		// See contrib/fuzz/oss_fuzz_build.sh
		// for more info.
		startDaemonForFuzzing(args)
	}()
	time.Sleep(time.Second * 4)
}

func printExecutions() {
	if r := recover(); r != nil {
		var err string
		switch r.(type) {
		case string:
			err = r.(string)
		case golangruntime.Error:
			err = r.(golangruntime.Error).Error()
		case error:
			err = r.(error).Error()
		default:
			err = "uknown error type"
		}
		fmt.Println("Executions:")
		for _, eo := range executionOrder {
			fmt.Println(eo)
		}
		panic(err)
	}
}

// FuzzCRI implements a fuzzer that tests CRI APIs.
func FuzzCRI(data []byte) int {
	initDaemon.Do(startDaemon)

	executionOrder = make([]string, 0)

	f := fuzz.NewConsumer(data)

	client, err := containerd.New(defaultAddress)
	if err != nil {
		return 0
	}
	defer client.Close()

	c, err := NewCRIService(criconfig.Config{}, client)
	if err != nil {
		panic(err)
	}

	calls, err := f.GetInt()
	if err != nil {
		return 0
	}

	defer printExecutions()
	for i := 0; i < calls%40; i++ {
		op, err := f.GetInt()
		if err != nil {
			return 0
		}
		opType := op % len(ops)

		switch ops[opType] {
		case "createContainer":
			createContainerFuzz(c.(*criService), f)
		case "removeContainer":
			removeContainerFuzz(c.(*criService), f)
		case "addSandboxes":
			addSandboxesFuzz(c.(*criService), f)
		case "listContainers":
			listContainersFuzz(c.(*criService), f)
		case "startContainer":
			startContainerFuzz(c.(*criService), f)
		case "containerStats":
			containerStatsFuzz(c.(*criService), f)
		case "listContainerStats":
			listContainerStatsFuzz(c.(*criService), f)
		case "containerStatus":
			containerStatusFuzz(c.(*criService), f)
		case "stopContainer":
			stopContainerFuzz(c.(*criService), f)
		case "updateContainerResources":
			updateContainerResourcesFuzz(c.(*criService), f)
		case "listImages":
			listImagesFuzz(c.(*criService), f)
		case "removeImages":
			removeImagesFuzz(c.(*criService), f)
		case "imageStatus":
			imageStatusFuzz(c.(*criService), f)
		case "imageFsInfo":
			imageFsInfoFuzz(c.(*criService), f)
		case "listPodSandbox":
			listPodSandboxFuzz(c.(*criService), f)
		case "portForward":
			portForwardFuzz(c.(*criService), f)
		case "removePodSandbox":
			removePodSandboxFuzz(c.(*criService), f)
		case "runPodSandbox":
			runPodSandboxFuzz(c.(*criService), f)
		case "podSandboxStatus":
			podSandboxStatusFuzz(c.(*criService), f)
		case "stopPodSandbox":
			stopPodSandboxFuzz(c.(*criService), f)
		case "status":
			statusFuzz(c.(*criService), f)
		case "updateRuntimeConfig":
			updateRuntimeConfigFuzz(c.(*criService), f)
		}
	}
	return 1
}

func logExecution(apiName, request string) {
	var logString strings.Builder
	logString.WriteString(fmt.Sprintf("Calling %s with \n %s \n\n", apiName, request))
	executionOrder = append(executionOrder, logString.String())
}

// createContainerFuzz creates a CreateContainerRequest and passes
// it to c.CreateContainer
func createContainerFuzz(c *criService, f *fuzz.ConsumeFuzzer) error {
	r := &runtime.CreateContainerRequest{}
	err := f.GenerateStruct(r)
	if err != nil {
		return err
	}
	_, _ = c.CreateContainer(context.Background(), r)
	reqString := fmt.Sprintf("%+v", r)
	logExecution("c.CreateContainer", reqString)
	return nil
}

// removeContainerFuzz creates a RemoveContainerRequest and passes
// it to c.RemoveContainer
func removeContainerFuzz(c *criService, f *fuzz.ConsumeFuzzer) error {
	r := &runtime.RemoveContainerRequest{}
	err := f.GenerateStruct(r)
	if err != nil {
		return err
	}
	_, _ = c.RemoveContainer(context.Background(), r)
	reqString := fmt.Sprintf("%+v", r)
	logExecution("c.RemoveContainer", reqString)
	return nil
}

// addSandboxesFuzz creates a sandbox and adds it to the sandboxstore
func addSandboxesFuzz(c *criService, f *fuzz.ConsumeFuzzer) error {
	quantity, err := f.GetInt()
	if err != nil {
		return err
	}
	for i := 0; i < quantity%20; i++ {
		newSandbox, err := getSandboxFuzz(f)
		if err != nil {
			return err
		}
		err = c.sandboxStore.Add(newSandbox)
		if err != nil {
			return err
		}
	}
	return nil
}

// getSandboxFuzz creates a sandbox
func getSandboxFuzz(f *fuzz.ConsumeFuzzer) (sandboxstore.Sandbox, error) {
	metadata := sandboxstore.Metadata{}
	status := sandboxstore.Status{}
	err := f.GenerateStruct(&metadata)
	if err != nil {
		return sandboxstore.Sandbox{}, err
	}
	err = f.GenerateStruct(&status)
	if err != nil {
		return sandboxstore.Sandbox{}, err
	}

	reqString := fmt.Sprintf("metadata: %+v\nstatus: %+v\n", metadata, status)
	logExecution("sandboxstore.NewSandbox", reqString)

	return sandboxstore.NewSandbox(metadata, status), nil
}

// listContainersFuzz creates a ListContainersRequest and passes
// it to c.ListContainers
func listContainersFuzz(c *criService, f *fuzz.ConsumeFuzzer) error {
	r := &runtime.ListContainersRequest{}
	err := f.GenerateStruct(r)
	if err != nil {
		return err
	}
	_, _ = c.ListContainers(context.Background(), r)
	reqString := fmt.Sprintf("%+v", r)
	logExecution("c.ListContainers", reqString)
	return nil
}

// startContainerFuzz creates a StartContainerRequest and passes
// it to c.StartContainer
func startContainerFuzz(c *criService, f *fuzz.ConsumeFuzzer) error {
	r := &runtime.StartContainerRequest{}
	err := f.GenerateStruct(r)
	if err != nil {
		return err
	}
	_, _ = c.StartContainer(context.Background(), r)
	reqString := fmt.Sprintf("%+v", r)
	logExecution("c.StartContainer", reqString)
	return nil
}

// containerStatsFuzz creates a ContainerStatsRequest and passes
// it to c.ContainerStats
func containerStatsFuzz(c *criService, f *fuzz.ConsumeFuzzer) error {
	r := &runtime.ContainerStatsRequest{}
	err := f.GenerateStruct(r)
	if err != nil {
		return err
	}
	_, _ = c.ContainerStats(context.Background(), r)
	reqString := fmt.Sprintf("%+v", r)
	logExecution("c.ContainerStats", reqString)
	return nil
}

// listContainerStatsFuzz creates a ListContainerStatsRequest and
// passes it to c.ListContainerStats
func listContainerStatsFuzz(c *criService, f *fuzz.ConsumeFuzzer) error {
	r := &runtime.ListContainerStatsRequest{}
	err := f.GenerateStruct(r)
	if err != nil {
		return err
	}
	_, _ = c.ListContainerStats(context.Background(), r)
	reqString := fmt.Sprintf("%+v", r)
	logExecution("c.ListContainerStats", reqString)
	return nil
}

// containerStatusFuzz creates a ContainerStatusRequest and passes
// it to c.ContainerStatus
func containerStatusFuzz(c *criService, f *fuzz.ConsumeFuzzer) error {
	r := &runtime.ContainerStatusRequest{}
	err := f.GenerateStruct(r)
	if err != nil {
		return err
	}
	_, _ = c.ContainerStatus(context.Background(), r)
	reqString := fmt.Sprintf("%+v", r)
	logExecution("c.ContainerStatus", reqString)
	return nil
}

// stopContainerFuzz creates a StopContainerRequest and passes
// it to c.StopContainer
func stopContainerFuzz(c *criService, f *fuzz.ConsumeFuzzer) error {
	r := &runtime.StopContainerRequest{}
	err := f.GenerateStruct(r)
	if err != nil {
		return err
	}
	_, _ = c.StopContainer(context.Background(), r)
	reqString := fmt.Sprintf("%+v", r)
	logExecution("c.StopContainer", reqString)
	return nil
}

// updateContainerResourcesFuzz creates a UpdateContainerResourcesRequest
// and passes it to c.UpdateContainerResources
func updateContainerResourcesFuzz(c *criService, f *fuzz.ConsumeFuzzer) error {
	r := &runtime.UpdateContainerResourcesRequest{}
	err := f.GenerateStruct(r)
	if err != nil {
		return err
	}
	_, _ = c.UpdateContainerResources(context.Background(), r)
	reqString := fmt.Sprintf("%+v", r)
	logExecution("c.UpdateContainerResources", reqString)
	return nil
}

// listImagesFuzz creates a ListImagesRequest and passes it to
// c.ListImages
func listImagesFuzz(c *criService, f *fuzz.ConsumeFuzzer) error {
	r := &runtime.ListImagesRequest{}
	err := f.GenerateStruct(r)
	if err != nil {
		return err
	}
	_, _ = c.ListImages(context.Background(), r)
	reqString := fmt.Sprintf("%+v", r)
	logExecution("c.ListImages", reqString)
	return nil
}

// removeImagesFuzz creates a RemoveImageRequest and passes it to
// c.RemoveImage
func removeImagesFuzz(c *criService, f *fuzz.ConsumeFuzzer) error {
	r := &runtime.RemoveImageRequest{}
	err := f.GenerateStruct(r)
	if err != nil {
		return err
	}
	_, _ = c.RemoveImage(context.Background(), r)
	reqString := fmt.Sprintf("%+v", r)
	logExecution("c.RemoveImage", reqString)
	return nil
}

// imageStatusFuzz creates an ImageStatusRequest and passes it to
// c.ImageStatus
func imageStatusFuzz(c *criService, f *fuzz.ConsumeFuzzer) error {
	r := &runtime.ImageStatusRequest{}
	err := f.GenerateStruct(r)
	if err != nil {
		return err
	}
	_, _ = c.ImageStatus(context.Background(), r)
	reqString := fmt.Sprintf("%+v", r)
	logExecution("c.ImageStatus", reqString)
	return nil
}

// imageFsInfoFuzz creates an ImageFsInfoRequest and passes it to
// c.ImageFsInfo
func imageFsInfoFuzz(c *criService, f *fuzz.ConsumeFuzzer) error {
	r := &runtime.ImageFsInfoRequest{}
	err := f.GenerateStruct(r)
	if err != nil {
		return err
	}
	_, _ = c.ImageFsInfo(context.Background(), r)
	reqString := fmt.Sprintf("%+v", r)
	logExecution("c.ImageFsInfo", reqString)
	return nil
}

// listPodSandboxFuzz creates a ListPodSandboxRequest and passes
// it to c.ListPodSandbox
func listPodSandboxFuzz(c *criService, f *fuzz.ConsumeFuzzer) error {
	r := &runtime.ListPodSandboxRequest{}
	err := f.GenerateStruct(r)
	if err != nil {
		return err
	}
	_, _ = c.ListPodSandbox(context.Background(), r)
	reqString := fmt.Sprintf("%+v", r)
	logExecution("c.ListPodSandbox", reqString)
	return nil
}

// portForwardFuzz creates a PortForwardRequest and passes it to
// c.PortForward
func portForwardFuzz(c *criService, f *fuzz.ConsumeFuzzer) error {
	r := &runtime.PortForwardRequest{}
	err := f.GenerateStruct(r)
	if err != nil {
		return err
	}
	_, _ = c.PortForward(context.Background(), r)
	reqString := fmt.Sprintf("%+v", r)
	logExecution("c.PortForward", reqString)
	return nil
}

// removePodSandboxFuzz creates a RemovePodSandboxRequest and
// passes it to c.RemovePodSandbox
func removePodSandboxFuzz(c *criService, f *fuzz.ConsumeFuzzer) error {
	r := &runtime.RemovePodSandboxRequest{}
	err := f.GenerateStruct(r)
	if err != nil {
		return err
	}
	_, _ = c.RemovePodSandbox(context.Background(), r)
	reqString := fmt.Sprintf("%+v", r)
	logExecution("c.RemovePodSandbox", reqString)
	return nil
}

// runPodSandboxFuzz creates a RunPodSandboxRequest and passes
// it to c.RunPodSandbox
func runPodSandboxFuzz(c *criService, f *fuzz.ConsumeFuzzer) error {
	r := &runtime.RunPodSandboxRequest{}
	err := f.GenerateStruct(r)
	if err != nil {
		return err
	}
	_, _ = c.RunPodSandbox(context.Background(), r)
	reqString := fmt.Sprintf("%+v", r)
	logExecution("c.RunPodSandbox", reqString)
	return nil
}

// podSandboxStatusFuzz creates a PodSandboxStatusRequest and
// passes it to
func podSandboxStatusFuzz(c *criService, f *fuzz.ConsumeFuzzer) error {
	r := &runtime.PodSandboxStatusRequest{}
	err := f.GenerateStruct(r)
	if err != nil {
		return err
	}
	_, _ = c.PodSandboxStatus(context.Background(), r)
	reqString := fmt.Sprintf("%+v", r)
	logExecution("c.PodSandboxStatus", reqString)
	return nil
}

// stopPodSandboxFuzz creates a StopPodSandboxRequest and passes
// it to c.StopPodSandbox
func stopPodSandboxFuzz(c *criService, f *fuzz.ConsumeFuzzer) error {
	r := &runtime.StopPodSandboxRequest{}
	err := f.GenerateStruct(r)
	if err != nil {
		return err
	}
	_, _ = c.StopPodSandbox(context.Background(), r)
	reqString := fmt.Sprintf("%+v", r)
	logExecution("c.StopPodSandbox", reqString)
	return nil
}

// statusFuzz creates a StatusRequest and passes it to c.Status
func statusFuzz(c *criService, f *fuzz.ConsumeFuzzer) error {
	r := &runtime.StatusRequest{}
	err := f.GenerateStruct(r)
	if err != nil {
		return err
	}
	_, _ = c.Status(context.Background(), r)
	reqString := fmt.Sprintf("%+v", r)
	logExecution("c.Status", reqString)
	return nil
}

func updateRuntimeConfigFuzz(c *criService, f *fuzz.ConsumeFuzzer) error {
	r := &runtime.UpdateRuntimeConfigRequest{}
	err := f.GenerateStruct(r)
	if err != nil {
		return err
	}
	_, _ = c.UpdateRuntimeConfig(context.Background(), r)
	reqString := fmt.Sprintf("%+v", r)
	logExecution("c.UpdateRuntimeConfig", reqString)
	return nil
}

// This creates a container directly in the store.
func getContainer(f *fuzz.ConsumeFuzzer) (containerstore.Container, error) {
	metadata := containerstore.Metadata{}
	status := containerstore.Status{}

	err := f.GenerateStruct(&metadata)
	if err != nil {
		return containerstore.Container{}, err
	}
	err = f.GenerateStruct(&status)
	if err != nil {
		return containerstore.Container{}, err
	}
	container, err := containerstore.NewContainer(metadata, containerstore.WithFakeStatus(status))
	return container, err
}

func newTestCRIServiceForFuzzing(f *fuzz.ConsumeFuzzer) *criService {
	labels := label.NewStore()

	return &criService{
		config:             testConfig,
		imageFSPath:        testImageFSPath,
		os:                 ostesting.NewFakeOS(),
		sandboxStore:       sandboxstore.NewStore(labels),
		imageStore:         imagestore.NewStore(nil),
		snapshotStore:      snapshotstore.NewStore(),
		sandboxNameIndex:   registrar.NewRegistrar(),
		containerStore:     containerstore.NewStore(labels),
		containerNameIndex: registrar.NewRegistrar(),
		netPlugin: map[string]cni.CNI{
			defaultNetworkPlugin: servertesting.NewFakeCNIPlugin(),
		},
	}
}
