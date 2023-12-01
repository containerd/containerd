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

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/oci"
	criconfig "github.com/containerd/containerd/v2/pkg/cri/config"
	"github.com/containerd/containerd/v2/pkg/cri/server"
	"github.com/containerd/containerd/v2/pkg/cri/server/base"
	"github.com/containerd/containerd/v2/pkg/cri/server/images"
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

	criBase := &base.CRIBase{
		Config:       config,
		BaseOCISpecs: map[string]*oci.Spec{},
	}

	imageService, err := images.NewService(config, client)
	if err != nil {
		panic(err)
	}

	c, err := server.NewCRIService(criBase, imageService, client, nil)
	if err != nil {
		panic(err)
	}

	return fuzzCRI(f, c)
}
