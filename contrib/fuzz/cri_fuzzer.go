//go:build gofuzz

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

package fuzz

import (
	"context"
	"fmt"
	golangruntime "runtime"
	"strings"

	fuzz "github.com/AdaLogics/go-fuzz-headers"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	"github.com/containerd/containerd/pkg/cri/server"
	"github.com/containerd/containerd/pkg/cri/server/images"
	containerstore "github.com/containerd/containerd/pkg/cri/store/container"
	sandboxstore "github.com/containerd/containerd/pkg/cri/store/sandbox"
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
	executionOrder []string
)

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

func fuzzCRI(f *fuzz.ConsumeFuzzer, c server.CRIService) int {
	executionOrder = make([]string, 0)
	defer printExecutions()

	calls, err := f.GetInt()
	if err != nil {
		return 0
	}

	executionOrder = make([]string, 0)
	defer printExecutions()

	for i := 0; i < calls%40; i++ {
		op, err := f.GetInt()
		if err != nil {
			return 0
		}
		opType := op % len(ops)

		switch ops[opType] {
		case "createContainer":
			createContainerFuzz(c, f)
		case "removeContainer":
			removeContainerFuzz(c, f)
		case "addSandboxes":
			addSandboxesFuzz(c, f)
		case "listContainers":
			listContainersFuzz(c, f)
		case "startContainer":
			startContainerFuzz(c, f)
		case "containerStats":
			containerStatsFuzz(c, f)
		case "listContainerStats":
			listContainerStatsFuzz(c, f)
		case "containerStatus":
			containerStatusFuzz(c, f)
		case "stopContainer":
			stopContainerFuzz(c, f)
		case "updateContainerResources":
			updateContainerResourcesFuzz(c, f)
		case "listImages":
			listImagesFuzz(c, f)
		case "removeImages":
			removeImagesFuzz(c, f)
		case "imageStatus":
			imageStatusFuzz(c, f)
		case "imageFsInfo":
			imageFsInfoFuzz(c, f)
		case "listPodSandbox":
			listPodSandboxFuzz(c, f)
		case "portForward":
			portForwardFuzz(c, f)
		case "removePodSandbox":
			removePodSandboxFuzz(c, f)
		case "runPodSandbox":
			runPodSandboxFuzz(c, f)
		case "podSandboxStatus":
			podSandboxStatusFuzz(c, f)
		case "stopPodSandbox":
			stopPodSandboxFuzz(c, f)
		case "status":
			statusFuzz(c, f)
		case "updateRuntimeConfig":
			updateRuntimeConfigFuzz(c, f)
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
func createContainerFuzz(c server.CRIService, f *fuzz.ConsumeFuzzer) error {
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
func removeContainerFuzz(c server.CRIService, f *fuzz.ConsumeFuzzer) error {
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

func sandboxStore(cs server.CRIService) (*sandboxstore.Store, error) {
	var (
		ss  *sandboxstore.Store
		err error
	)

	ss, err = server.SandboxStore(cs)
	if err != nil {
		ss, err = server.SandboxStore(cs)
		if err != nil {
			return nil, err
		}
		return ss, nil
	}
	return ss, nil
}

// addSandboxesFuzz creates a sandbox and adds it to the sandboxstore
func addSandboxesFuzz(c server.CRIService, f *fuzz.ConsumeFuzzer) error {
	quantity, err := f.GetInt()
	if err != nil {
		return err
	}

	ss, err := sandboxStore(c)
	if err != nil {
		return err
	}

	for i := 0; i < quantity%20; i++ {
		newSandbox, err := getSandboxFuzz(f)
		if err != nil {
			return err
		}
		err = ss.Add(newSandbox)
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
func listContainersFuzz(c server.CRIService, f *fuzz.ConsumeFuzzer) error {
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
func startContainerFuzz(c server.CRIService, f *fuzz.ConsumeFuzzer) error {
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
func containerStatsFuzz(c server.CRIService, f *fuzz.ConsumeFuzzer) error {
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
func listContainerStatsFuzz(c server.CRIService, f *fuzz.ConsumeFuzzer) error {
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
func containerStatusFuzz(c server.CRIService, f *fuzz.ConsumeFuzzer) error {
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
func stopContainerFuzz(c server.CRIService, f *fuzz.ConsumeFuzzer) error {
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
func updateContainerResourcesFuzz(c server.CRIService, f *fuzz.ConsumeFuzzer) error {
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
func listImagesFuzz(c server.CRIService, f *fuzz.ConsumeFuzzer) error {
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
func removeImagesFuzz(c server.CRIService, f *fuzz.ConsumeFuzzer) error {
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
func imageStatusFuzz(c server.CRIService, f *fuzz.ConsumeFuzzer) error {
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
func imageFsInfoFuzz(c server.CRIService, f *fuzz.ConsumeFuzzer) error {
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
func listPodSandboxFuzz(c server.CRIService, f *fuzz.ConsumeFuzzer) error {
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
func portForwardFuzz(c server.CRIService, f *fuzz.ConsumeFuzzer) error {
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
func removePodSandboxFuzz(c server.CRIService, f *fuzz.ConsumeFuzzer) error {
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
func runPodSandboxFuzz(c server.CRIService, f *fuzz.ConsumeFuzzer) error {
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
func podSandboxStatusFuzz(c server.CRIService, f *fuzz.ConsumeFuzzer) error {
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
func stopPodSandboxFuzz(c server.CRIService, f *fuzz.ConsumeFuzzer) error {
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
func statusFuzz(c server.CRIService, f *fuzz.ConsumeFuzzer) error {
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

func updateRuntimeConfigFuzz(c server.CRIService, f *fuzz.ConsumeFuzzer) error {
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

func FuzzParseAuth(data []byte) int {
	f := fuzz.NewConsumer(data)
	auth := &runtime.AuthConfig{}
	err := f.GenerateStruct(auth)
	if err != nil {
		return 0
	}
	host, err := f.GetString()
	if err != nil {
		return 0
	}
	_, _, _ = images.ParseAuth(auth, host)
	return 1
}
