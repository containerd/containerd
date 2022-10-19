//go:build !windows
// +build !windows

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
	"github.com/pkg/errors"
	"github.com/urfave/cli"
)

var platformCreateFlags = []cli.Flag{
	cli.Int64Flag{
		Name:  "cpu-quota",
		Usage: "Limit CPU CFS quota",
		Value: -1,
	}, cli.Uint64Flag{
		Name:  "cpu-period",
		Usage: "Limit CPU CFS period",
	},
	cli.Float64Flag{
		Name:  "cpus",
		Usage: "set the CFS cpu quota",
		Value: 0.0,
	},
}

func platformSpecOpts(context *cli.Context) ([]containerd.SimpleSpecOpts, error) {
	var specOpts []containerd.SimpleSpecOpts
	if cpus := context.Float64("cpus"); cpus > 0.0 {
		var (
			period = uint64(100000)
			quota  = int64(cpus * 100000.0)
		)
		specOpts = append(specOpts, containerd.Simple(oci.WithCPUCFS(quota, period)))
	}

	quota := context.Int64("cpu-quota")
	period := context.Uint64("cpu-period")
	if quota != -1 || period != 0 {
		if cpus := context.Float64("cpus"); cpus > 0.0 {
			return nil, errors.New("cpus and quota/period should be used separately")
		}
		specOpts = append(specOpts, containerd.Simple(oci.WithCPUCFS(quota, period)))
	}
	return specOpts, nil
}
