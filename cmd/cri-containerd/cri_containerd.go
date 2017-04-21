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

package main

import (
	"github.com/golang/glog"
	"github.com/spf13/pflag"

	"github.com/kubernetes-incubator/cri-containerd/cmd/cri-containerd/options"
	"github.com/kubernetes-incubator/cri-containerd/pkg/server"
)

func main() {
	o := options.NewCRIContainerdOptions()
	o.AddFlags(pflag.CommandLine)
	options.InitFlags()

	glog.V(2).Infof("Connect to containerd socket %q with timeout %v", o.ContainerdSocketPath, o.ContainerdConnectionTimeout)
	conn, err := server.ConnectToContainerd(o.ContainerdSocketPath, o.ContainerdConnectionTimeout)
	if err != nil {
		glog.Exitf("Failed to connect containerd socket %q: %v", o.ContainerdSocketPath, err)
	}

	glog.V(2).Infof("Run cri-containerd grpc server on socket %q", o.CRIContainerdSocketPath)
	service := server.NewCRIContainerdService(conn)
	s := server.NewCRIContainerdServer(o.CRIContainerdSocketPath, service, service)
	if err := s.Run(); err != nil {
		glog.Exitf("Failed to run cri-containerd grpc server: %v", err)
	}
}
