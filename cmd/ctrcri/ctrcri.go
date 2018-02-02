/*
Copyright 2018 The Containerd Authors.

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
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/containerd/cri-containerd/cmd/cri-containerd/options"
	"github.com/containerd/cri-containerd/pkg/version"
)

const (
	// Add \u200B to avoid the space trimming.
	desc = "\u200B" + `        __                 _ 
  _____/ /________________(_)
 / ___/ __/ ___/ ___/ ___/ / 
/ /__/ /_/ /  / /__/ /  / /  
\___/\__/_/   \___/_/  /_/   

containerd CRI plugin CLI
`
	command = "ctrcri"
)

var cmd = &cobra.Command{
	Use:   command,
	Short: "A CLI for containerd CRI plugin.",
	Long:  desc,
}

var (
	// address is the address for containerd's GRPC server.
	address string
	// timeout is the timeout for containerd grpc connection.
	timeout time.Duration
	// defaultTimeout is the default timeout for containerd grpc connection.
	defaultTimeout = 10 * time.Second
)

func addGlobalFlags(fs *pflag.FlagSet) {
	// TODO(random-liu): Change default to containerd/defaults.DefaultAddress after cri plugin
	// become default.
	fs.StringVar(&address, "address", options.DefaultConfig().SocketPath, "address for containerd's GRPC server.")
	fs.DurationVar(&timeout, "timeout", defaultTimeout, "timeout for containerd grpc connection.")
}

func versionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print " + command + " version information.",
		Run: func(cmd *cobra.Command, args []string) {
			version.PrintVersion()
		},
	}
}

func main() {
	addGlobalFlags(cmd.PersistentFlags())

	cmd.AddCommand(versionCommand())
	cmd.AddCommand(loadImageCommand())
	if err := cmd.Execute(); err != nil {
		// Error should have been reported.
		os.Exit(1)
	}
}
