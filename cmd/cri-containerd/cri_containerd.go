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

	"github.com/docker/docker/pkg/reexec"
	"github.com/golang/glog"
	"github.com/opencontainers/selinux/go-selinux"
	"github.com/spf13/pflag"
	"k8s.io/kubernetes/pkg/util/interrupt"

	"github.com/kubernetes-incubator/cri-containerd/cmd/cri-containerd/options"
	"github.com/kubernetes-incubator/cri-containerd/pkg/server"
	"github.com/kubernetes-incubator/cri-containerd/pkg/version"
)

func main() {
	if reexec.Init() {
		return
	}
	o := options.NewCRIContainerdOptions()
	o.AddFlags(pflag.CommandLine)
	if err := o.InitFlags(pflag.CommandLine); err != nil {
		glog.Exitf("Failed to init CRI containerd flags: %v", err)
	}

	glog.V(0).Infof("Run cri-containerd %+v", o)
	if o.PrintVersion {
		version.PrintVersion()
		os.Exit(0)
	}

	if o.PrintDefaultConfig {
		o.PrintDefaultTomlConfig()
		os.Exit(0)
	}

	validateConfig(o)

	glog.V(2).Infof("Run cri-containerd grpc server on socket %q", o.SocketPath)
	s, err := server.NewCRIContainerdService(o.Config)
	if err != nil {
		glog.Exitf("Failed to create CRI containerd service: %v", err)
	}
	// Use interrupt handler to make sure the server to be stopped properly.
	// Pass in non-empty final function to avoid os.Exit(1). We expect `Run`
	// to return itself.
	h := interrupt.New(func(os.Signal) {}, s.Stop)
	if err := h.Run(func() error { return s.Run() }); err != nil {
		glog.Exitf("Failed to run cri-containerd grpc server: %v", err)
	}
}

func validateConfig(o *options.CRIContainerdOptions) {
	if o.EnableSelinux {
		if !selinux.GetEnabled() {
			glog.Warning("Selinux is not supported")
		}
	} else {
		selinux.SetDisabled()
	}
}
