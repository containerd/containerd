//go:build gofuzz
// +build gofuzz

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
	"sync"
	"time"

	"github.com/containerd/containerd/cmd/containerd/command"
)

var (
	initDaemon sync.Once
)

func startDaemonForFuzzing(arguments []string) {
	app := command.App()
	_ = app.Run(arguments)
}

func startDaemon() {
	args := []string{"--log-level", "debug"}
	go func() {
		// This is similar to invoking the
		// containerd binary.
		// See contrib/fuzz/oss_fuzz_build.sh
		// for more info.
		startDaemonForFuzzing(args)
	}()
	time.Sleep(time.Second * 4)
}
