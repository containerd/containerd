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

package client

import (
	"context"
	"flag"
	"fmt"
	"os"
	"testing"

	"github.com/containerd/containerd/v2/defaults"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/containerd/log/logtest"
)

const (
	testNamespace = "testing"
)

//nolint:unused // some variables used in fuzz but not all platforms
var (
	address           string
	ctrdStdioFilePath string
	testSnapshotter   = defaults.DefaultSnapshotter
	ctrd              = &daemon{}
)

func init() {
	flag.StringVar(&address, "address", defaults.DefaultAddress, "The address to the containerd socket for use in the tests")
}

func testContext(t testing.TB) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	ctx = namespaces.WithNamespace(ctx, testNamespace)
	if t != nil {
		ctx = logtest.WithT(ctx, t)
	}
	return ctx, cancel
}

func createShimDebugConfig() string {
	f, err := os.CreateTemp("", "containerd-config-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create config file: %s\n", err)
		os.Exit(1)
	}
	defer f.Close()
	if _, err := f.WriteString("version = 2\n"); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to write to config file %s: %s\n", f.Name(), err)
		os.Exit(1)
	}

	if _, err := f.WriteString("[plugins.\"io.containerd.runtime.v1.linux\"]\n\tshim_debug = true\n"); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to write to config file %s: %s\n", f.Name(), err)
		os.Exit(1)
	}

	return f.Name()
}
