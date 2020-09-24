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

package main

import (
	"fmt"
	"math/rand"
	"os"
	"time"

	_ "github.com/containerd/containerd/diff/walking/plugin"
	_ "github.com/containerd/containerd/gc/scheduler"
	_ "github.com/containerd/containerd/runtime/v2"
	_ "github.com/containerd/containerd/services/containers"
	_ "github.com/containerd/containerd/services/content"
	_ "github.com/containerd/containerd/services/diff"
	_ "github.com/containerd/containerd/services/events"
	_ "github.com/containerd/containerd/services/healthcheck"
	_ "github.com/containerd/containerd/services/images"
	_ "github.com/containerd/containerd/services/introspection"
	_ "github.com/containerd/containerd/services/leases"
	_ "github.com/containerd/containerd/services/namespaces"
	_ "github.com/containerd/containerd/services/snapshots"
	_ "github.com/containerd/containerd/services/tasks"
	_ "github.com/containerd/containerd/services/version"
	_ "github.com/containerd/cri"

	"github.com/containerd/containerd/cmd/containerd/command"
	"github.com/sirupsen/logrus"
)

func init() {
	rand.Seed(time.Now().UnixNano())
}

func main() {
	app := command.App()
	logrus.Warn("This customized containerd is only for CI test, DO NOT use it for distribution.")
	if err := app.Run(os.Args); err != nil {
		fmt.Fprintf(os.Stderr, "containerd: %s\n", err)
		os.Exit(1)
	}
}
