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

package sandboxes

import (
	"github.com/containerd/containerd"
	"github.com/containerd/containerd/oci"
	"github.com/urfave/cli"
)

var platformCreateFlags = []cli.Flag{
	cli.Uint64Flag{
		Name:  "cpu-count",
		Usage: "number of CPUs available to the sandbox",
	},
	cli.Uint64Flag{
		Name:  "cpu-shares",
		Usage: "The relative number of CPU shares given to the sandbox relative to other workloads. Between 0 and 10,000.",
	},
	cli.Uint64Flag{
		Name:  "cpu-max",
		Usage: "The number of processor cycles threads in a sandbox can use per 10,000 cycles. Set to a percentage times 100. Between 1 and 10,000",
	},
}

func platformSpecOpts(context *cli.Context) ([]containerd.SimpleSpecOpts, error) {
	var opts []containerd.SimpleSpecOpts
	ccount := context.Uint64("cpu-count")
	if ccount != 0 {
		opts = append(opts, containerd.Simple(oci.WithWindowsCPUCount(ccount)))
	}
	cshares := context.Uint64("cpu-shares")
	if cshares != 0 {
		opts = append(opts, containerd.Simple(oci.WithWindowsCPUShares(uint16(cshares))))
	}
	cmax := context.Uint64("cpu-max")
	if cmax != 0 {
		opts = append(opts, containerd.Simple(oci.WithWindowsCPUMaximum(uint16(cmax))))
	}
	return opts, nil
}
