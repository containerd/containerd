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

package commands

import (
	"github.com/urfave/cli"
)

func init() {
	ContainerFlags = append(ContainerFlags,
		cli.Uint64Flag{
			Name:  "cpu-count",
			Usage: "Number of CPUs available to the container",
		},
		cli.Uint64Flag{
			Name:  "cpu-shares",
			Usage: "The relative number of CPU shares given to the container relative to other workloads. Between 0 and 10,000.",
		},
		cli.Uint64Flag{
			Name:  "cpu-max",
			Usage: "The number of processor cycles threads in a container can use per 10,000 cycles. Set to a percentage times 100. Between 1 and 10,000",
		}, cli.StringSliceFlag{
			Name:  "device",
			Usage: "Identifier of a device to add to the container  (e.g. class://5B45201D-F2F2-4F3B-85BB-30FF1F953599)",
		})
}
