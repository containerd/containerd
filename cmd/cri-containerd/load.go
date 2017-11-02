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
	"fmt"
	"path/filepath"
	"strings"

	dedentutil "github.com/renstrom/dedent"
	"github.com/spf13/cobra"
	"golang.org/x/net/context"

	"github.com/kubernetes-incubator/cri-containerd/cmd/cri-containerd/options"
	api "github.com/kubernetes-incubator/cri-containerd/pkg/api/v1"
	"github.com/kubernetes-incubator/cri-containerd/pkg/client"
)

func dedent(s string) string {
	return strings.TrimLeft(dedentutil.Dedent(s), "\n")
}

var (
	loadLong = dedent(`
		Help for "load TAR" command

		TAR - The path to a tar archive containing a container image.

		Requirement:
			Containerd and cri-containerd daemons (grpc servers) must already be up and
			running before the load command is used.

		Description:
			Running as a client, cri-containerd implements the load command by sending the
			load request to the already running cri-containerd daemon/server, which in
			turn loads the image into containerd's image storage via the already running
			containerd daemon.`)

	loadExample = dedent(`
		- use docker to pull the latest busybox image and save it as a tar archive:
			$ docker pull busybox:latest
			$ docker save busybox:latest -o busybox.tar
		- use cri-containerd to load the image into containerd's image storage:
			$ cri-containerd load busybox.tar`)
)

func loadImageCommand() *cobra.Command {
	c := &cobra.Command{
		Use:     "load TAR",
		Long:    loadLong,
		Short:   "Load an image from a tar archive.",
		Args:    cobra.ExactArgs(1),
		Example: loadExample,
	}
	c.SetUsageTemplate(strings.Replace(c.UsageTemplate(), "Examples:", "Example:", 1))
	endpoint, timeout := options.AddGRPCFlags(c.Flags())
	c.RunE = func(cmd *cobra.Command, args []string) error {
		cl, err := client.NewCRIContainerdClient(*endpoint, *timeout)
		if err != nil {
			return fmt.Errorf("failed to create grpc client: %v", err)
		}
		path, err := filepath.Abs(args[0])
		if err != nil {
			return fmt.Errorf("failed to get absolute path: %v", err)
		}
		res, err := cl.LoadImage(context.Background(), &api.LoadImageRequest{FilePath: path})
		if err != nil {
			return fmt.Errorf("failed to load image: %v", err)
		}
		images := res.GetImages()
		for _, image := range images {
			fmt.Println("Loaded image:", image)
		}
		return nil
	}
	return c
}
