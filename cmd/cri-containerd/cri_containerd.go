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
	"os"

	"github.com/golang/glog"
	"github.com/spf13/pflag"

	"github.com/kubernetes-incubator/cri-containerd/cmd/cri-containerd/options"
	"github.com/kubernetes-incubator/cri-containerd/pkg/server"
	"github.com/kubernetes-incubator/cri-containerd/pkg/version"
)

func main() {
	o := options.NewCRIContainerdOptions()
	o.AddFlags(pflag.CommandLine)
	options.InitFlags()

	if o.PrintVersion {
		version.PrintVersion()
		os.Exit(0)
	}

	glog.V(2).Infof("Run cri-containerd grpc server on socket %q", o.SocketPath)
	service, err := server.NewCRIContainerdService(
		o.ContainerdEndpoint,
		o.ContainerdSnapshotter,
		o.RootDir,
		o.NetworkPluginBinDir,
		o.NetworkPluginConfDir,
		o.StreamServerAddress,
		o.StreamServerPort,
		o.CgroupPath,
	)
	if err != nil {
		glog.Exitf("Failed to create CRI containerd service %+v: %v", o, err)
	}
	service.Start()

	s := server.NewCRIContainerdServer(o.SocketPath, service, service)
	if err := s.Run(); err != nil {
		glog.Exitf("Failed to run cri-containerd grpc server: %v", err)
	}
}
