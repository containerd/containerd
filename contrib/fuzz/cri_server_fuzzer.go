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
	fuzz "github.com/AdaLogics/go-fuzz-headers"
	"google.golang.org/grpc"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	containerd "github.com/containerd/containerd/v2/client"
	criconfig "github.com/containerd/containerd/v2/pkg/cri/config"
	"github.com/containerd/containerd/v2/pkg/cri/instrument"
	"github.com/containerd/containerd/v2/pkg/cri/server"
	"github.com/containerd/containerd/v2/pkg/cri/server/images"
	"github.com/containerd/containerd/v2/pkg/oci"
)

func FuzzCRIServer(data []byte) int {
	initDaemon.Do(startDaemon)

	f := fuzz.NewConsumer(data)

	client, err := containerd.New(defaultAddress)
	if err != nil {
		return 0
	}
	defer client.Close()

	config := criconfig.Config{}
	imageConfig := criconfig.ImageConfig{}

	imageService, err := images.NewService(imageConfig, &images.CRIImageServiceOptions{
		Client: client,
	})
	if err != nil {
		panic(err)
	}

	c, rs, err := server.NewCRIService(config, &server.CRIServiceOptions{
		ImageService: imageService,
		Client:       client,
		BaseOCISpecs: map[string]*oci.Spec{},
	})
	if err != nil {
		panic(err)
	}

	return fuzzCRI(f, &service{
		CRIService:           c,
		RuntimeServiceServer: rs,
		ImageServiceServer:   imageService.GRPCService(),
	})
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
