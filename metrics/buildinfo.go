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

package metrics

import (
	"github.com/containerd/containerd/version"
	goMetrics "github.com/docker/go-metrics"
	"github.com/prometheus/client_golang/prometheus"
)

func newBuildInfoCollector(ns *goMetrics.Namespace) *Collector {
	return &Collector{
		ns: ns,
		m: metric{
			name: "build_info",
			help: "Build information on containerd",
			unit: goMetrics.Total,
			vt:   prometheus.CounterValue,
			labels: []string{
				"version",
				"revision",
			},
			getValues: func() []value {
				return []value{
					{
						l: []string{
							version.Version,
							version.Revision,
						},
						v: float64(1),
					},
				}
			},
		},
	}
}
