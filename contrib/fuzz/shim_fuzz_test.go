//go:build !windows

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
	"context"
	"net/url"
	"os"
	"testing"

	fuzz "github.com/AdaLogics/go-fuzz-headers"
	"github.com/containerd/containerd/v2/cmd/containerd-shim-runc-v2/process"
	"github.com/containerd/containerd/v2/pkg/namespaces"
)

func FuzzNewBinaryIO(f *testing.F) {
	f.Fuzz(func(t *testing.T, data []byte) {
		ff := fuzz.NewConsumer(data)

		// Generate random ID and namespace
		id, err := ff.GetString()
		if err != nil {
			return
		}
		ns, err := ff.GetString()
		if err != nil {
			return
		}
		if ns == "" {
			ns = "fuzz"
		}

		// Generate random URI
		uriStr, err := ff.GetString()
		if err != nil {
			return
		}
		u, err := url.Parse(uriStr)
		if err != nil {
			return
		}

		// Test NewBinaryCmd
		cmd := process.NewBinaryCmd(u, id, ns)
		if cmd == nil {
			return
		}

		// Test NewBinaryIO (only for "binary" scheme)
		if u.Scheme == "binary" {
			// Ensure we don't actually execute random binaries from fuzzer input
			// if they happen to exist on the system.
			// We override the path to something safe like /bin/true if it exists,
			// or a non-existent path.
			originalPath := u.Path

			// Probability-based choice: 50% use /bin/true (if exists), 50% use random path
			useTrue, err := ff.GetBool()
			if err == nil && useTrue {
				if _, err := os.Stat("/bin/true"); err == nil {
					u.Path = "/bin/true"
				}
			}

			// We need a context with a namespace
			ctx := namespaces.WithNamespace(context.Background(), ns)

			io, err := process.NewBinaryIO(ctx, id, u)
			if err == nil && io != nil {
				io.Close()
			}

			// Restore path for further fuzzing if u is reused
			u.Path = originalPath
		}
	})
}
