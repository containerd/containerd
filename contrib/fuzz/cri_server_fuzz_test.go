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
	"os"
	golangruntime "runtime"
	"strings"
	"testing"

	fuzz "github.com/AdaLogics/go-fuzz-headers"
	containerd "github.com/containerd/containerd/v2/client"
	criconfig "github.com/containerd/containerd/v2/internal/cri/config"
	"github.com/containerd/containerd/v2/internal/cri/instrument"
	"github.com/containerd/containerd/v2/internal/cri/server"
	"github.com/containerd/containerd/v2/internal/cri/server/images"
	"github.com/containerd/containerd/v2/pkg/oci"
	"github.com/containerd/errdefs"
	"google.golang.org/grpc"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

func FuzzCRIServer(f *testing.F) {
	if os.Getuid() != 0 {
		f.Skip("skipping fuzz test that requires root")
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		initDaemon.Do(startDaemon)

		f := fuzz.NewConsumer(data)

		client, err := containerd.New(defaultAddress)
		if err != nil {
			t.Fatal(err)
		}
		defer client.Close()

		imageConfig := criconfig.ImageConfig{}

		imageService, err := images.NewService(imageConfig, &images.CRIImageServiceOptions{
			Client: client,
		})
		if err != nil {
			t.Fatal(err)
		}

		c, rs, err := server.NewCRIService(&server.CRIServiceOptions{
			RuntimeService: &fakeRuntimeService{},
			ImageService:   imageService,
			Client:         client,
		})
		if err != nil {
			t.Fatal(err)
		}

		fuzzCRI(t, f, &service{
			CRIService:           c,
			RuntimeServiceServer: rs,
			ImageServiceServer:   imageService.GRPCService(),
		})
	})
}

type fakeRuntimeService struct{}

func (fakeRuntimeService) Config() criconfig.Config {
	return criconfig.Config{}
}

func (fakeRuntimeService) LoadOCISpec(string) (*oci.Spec, error) {
	return nil, errdefs.ErrNotFound
}

type service struct {
	server.CRIService
	runtime.RuntimeServiceServer
	runtime.ImageServiceServer
}

func (c *service) Register(s *grpc.Server) error {
	instrumented := instrument.NewService(c)
	runtime.RegisterRuntimeServiceServer(s, instrumented)
	runtime.RegisterImageServiceServer(s, instrumented)
	return nil
}

var (
	executionOrder []string
)

func printExecutions(t *testing.T) {
	if r := recover(); r != nil {
		var err string
		switch res := r.(type) {
		case string:
			err = res
		case golangruntime.Error:
			err = res.Error()
		case error:
			err = res.Error()
		default:
			err = "uknown error type"
		}
		t.Log("Executions:")
		for _, eo := range executionOrder {
			t.Log(eo)
		}
		t.Fatal(err)
	}
}

type fuzzCRIService interface {
	server.CRIService
	runtime.RuntimeServiceServer
	runtime.ImageServiceServer
}

func fuzzCRI(t *testing.T, f *fuzz.ConsumeFuzzer, c fuzzCRIService) int {
	ops := []func(c fuzzCRIService, f *fuzz.ConsumeFuzzer) error{
		createContainerFuzz,
		removeContainerFuzz,
		listContainersFuzz,
		startContainerFuzz,
		containerStatsFuzz,
		listContainerStatsFuzz,
		containerStatusFuzz,
		stopContainerFuzz,
		updateContainerResourcesFuzz,
		listImagesFuzz,
		removeImagesFuzz,
		imageStatusFuzz,
		imageFsInfoFuzz,
		listPodSandboxFuzz,
		portForwardFuzz,
		removePodSandboxFuzz,
		runPodSandboxFuzz,
		podSandboxStatusFuzz,
		stopPodSandboxFuzz,
		statusFuzz,
		updateRuntimeConfigFuzz,
	}

	calls, err := f.GetInt()
	if err != nil {
		return 0
	}

	executionOrder = make([]string, 0)
	defer printExecutions(t)

	for i := 0; i < calls%40; i++ {
		op, err := f.GetInt()
		if err != nil {
			return 0
		}
		ops[op%len(ops)](c, f)
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
func createContainerFuzz(c fuzzCRIService, f *fuzz.ConsumeFuzzer) error {
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
func removeContainerFuzz(c fuzzCRIService, f *fuzz.ConsumeFuzzer) error {
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

// listContainersFuzz creates a ListContainersRequest and passes
// it to c.ListContainers
func listContainersFuzz(c fuzzCRIService, f *fuzz.ConsumeFuzzer) error {
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
func startContainerFuzz(c fuzzCRIService, f *fuzz.ConsumeFuzzer) error {
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
func containerStatsFuzz(c fuzzCRIService, f *fuzz.ConsumeFuzzer) error {
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
func listContainerStatsFuzz(c fuzzCRIService, f *fuzz.ConsumeFuzzer) error {
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
func containerStatusFuzz(c fuzzCRIService, f *fuzz.ConsumeFuzzer) error {
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
func stopContainerFuzz(c fuzzCRIService, f *fuzz.ConsumeFuzzer) error {
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
func updateContainerResourcesFuzz(c fuzzCRIService, f *fuzz.ConsumeFuzzer) error {
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
func listImagesFuzz(c fuzzCRIService, f *fuzz.ConsumeFuzzer) error {
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
func removeImagesFuzz(c fuzzCRIService, f *fuzz.ConsumeFuzzer) error {
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
func imageStatusFuzz(c fuzzCRIService, f *fuzz.ConsumeFuzzer) error {
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
func imageFsInfoFuzz(c fuzzCRIService, f *fuzz.ConsumeFuzzer) error {
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
func listPodSandboxFuzz(c fuzzCRIService, f *fuzz.ConsumeFuzzer) error {
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
func portForwardFuzz(c fuzzCRIService, f *fuzz.ConsumeFuzzer) error {
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
func removePodSandboxFuzz(c fuzzCRIService, f *fuzz.ConsumeFuzzer) error {
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
func runPodSandboxFuzz(c fuzzCRIService, f *fuzz.ConsumeFuzzer) error {
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
func podSandboxStatusFuzz(c fuzzCRIService, f *fuzz.ConsumeFuzzer) error {
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
func stopPodSandboxFuzz(c fuzzCRIService, f *fuzz.ConsumeFuzzer) error {
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
func statusFuzz(c fuzzCRIService, f *fuzz.ConsumeFuzzer) error {
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

func updateRuntimeConfigFuzz(c fuzzCRIService, f *fuzz.ConsumeFuzzer) error {
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
