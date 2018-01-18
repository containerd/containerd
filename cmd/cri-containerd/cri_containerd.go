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
	"flag"
	"fmt"
	"net"
	"net/http"
	"net/http/pprof"
	"os"
	"os/signal"
	"runtime"
	"syscall"

	"github.com/opencontainers/selinux/go-selinux"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"k8s.io/kubernetes/pkg/util/interrupt"

	"github.com/containerd/cri-containerd/cmd/cri-containerd/options"
	"github.com/containerd/cri-containerd/pkg/log"
	"github.com/containerd/cri-containerd/pkg/server"
	"github.com/containerd/cri-containerd/pkg/version"
)

// Add \u200B to avoid the space trimming.
const desc = "\u200B" + `             _                         __        _                      __
  __________(_)      _________  ____  / /_____ _(_)____  ___  _________/ /
 / ___/ ___/ /______/ ___/ __ \/ __ \/ __/ __ ` + "`" + `/ // __ \/ _ \/ ___/ __  /
/ /__/ /  / //_____/ /__/ /_/ / / / / /_/ /_/ / // / / /  __/ /  / /_/ /
\___/_/  /_/       \___/\____/_/ /_/\__/\__,_/_//_/ /_/\___/_/   \__,_/

A containerd based Kubernetes CRI implementation.
`

var cmd = &cobra.Command{
	Use:   "cri-containerd",
	Short: "A containerd based Kubernetes CRI implementation.",
	Long:  desc,
}

func defaultConfigCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "default-config",
		Short: "Print default toml config of cri-containerd.",
		Run: func(cmd *cobra.Command, args []string) {
			options.PrintDefaultTomlConfig()
		},
	}
}

func versionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print cri-containerd version information.",
		Run: func(cmd *cobra.Command, args []string) {
			version.PrintVersion()
		},
	}
}

func main() {
	o := options.NewCRIContainerdOptions()

	o.AddFlags(cmd.Flags())
	cmd.AddCommand(defaultConfigCommand())
	cmd.AddCommand(versionCommand())
	cmd.AddCommand(loadImageCommand())

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		setupDumpStacksTrap()
		if err := o.InitFlags(cmd.Flags()); err != nil {
			return fmt.Errorf("failed to init CRI containerd flags: %v", err)
		}
		validateConfig(o)

		if err := setLogLevel(o.LogLevel); err != nil {
			return fmt.Errorf("failed to set log level: %v", err)
		}

		logrus.Infof("Run cri-containerd %+v", o)

		// Start profiling server if enable.
		if o.EnableProfiling {
			logrus.Info("Start profiling server")
			go startProfilingServer(o.ProfilingAddress, o.ProfilingPort)
		}

		logrus.Infof("Run cri-containerd grpc server on socket %q", o.SocketPath)
		s, err := server.NewCRIContainerdService(o.Config)
		if err != nil {
			return fmt.Errorf("failed to create CRI containerd service: %v", err)
		}
		// Use interrupt handler to make sure the server is stopped properly.
		// Pass in non-empty final function to avoid os.Exit(1). We expect `Run`
		// to return itself.
		h := interrupt.New(func(os.Signal) {}, s.Stop)
		if err := h.Run(func() error { return s.Run() }); err != nil {
			return fmt.Errorf("failed to run cri-containerd grpc server: %v", err)
		}
		return nil
	}

	if err := cmd.Execute(); err != nil {
		// Error should have been reported.
		os.Exit(1)
	}
}

func validateConfig(o *options.CRIContainerdOptions) {
	if o.EnableSelinux {
		if !selinux.GetEnabled() {
			logrus.Warn("Selinux is not supported")
		}
	} else {
		selinux.SetDisabled()
	}
}

func setupDumpStacksTrap() {
	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGUSR1)
	go func() {
		for range c {
			dumpStacks()
		}
	}()
}

func dumpStacks() {
	buf := make([]byte, 1024)
	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			buf = buf[:n]
			break
		}
		buf = make([]byte, 2*len(buf))
	}
	logrus.Infof("=== BEGIN goroutine stack dump ===\n%s\n=== END goroutine stack dump ===", buf)
}

// startProfilingServer start http server to profiling via web interface
func startProfilingServer(host string, port string) {
	endpoint := net.JoinHostPort(host, port)
	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	if err := http.ListenAndServe(endpoint, mux); err != nil {
		logrus.WithError(err).Error("Failed to start profiling server")
	}
}

func setLogLevel(l string) error {
	lvl, err := log.ParseLevel(l)
	if err != nil {
		return err
	}
	if err := setGLogLevel(lvl); err != nil {
		return err
	}
	logrus.SetLevel(lvl)
	return nil
}

// TODO(random-liu): Set glog level in plugin mode.
func setGLogLevel(l logrus.Level) error {
	if err := flag.Set("logtostderr", "true"); err != nil {
		return err
	}
	switch l {
	case log.TraceLevel:
		return flag.Set("v", "5")
	case logrus.DebugLevel:
		return flag.Set("v", "4")
	case logrus.InfoLevel:
		return flag.Set("v", "2")
	// glog doesn't support following filters. Defaults to v=0.
	case logrus.WarnLevel:
	case logrus.ErrorLevel:
	case logrus.FatalLevel:
	case logrus.PanicLevel:
	}
	return nil
}
