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
	metrics "github.com/docker/go-metrics"
)

var (
	buildInfoLabeledCounter metrics.LabeledCounter
)

func init() {
	ns := metrics.NewNamespace("containerd", "", nil)
	buildInfoLabeledCounter = ns.NewLabeledCounter("build_info", "containerd build information", "version", "revision")
	buildInfoLabeledCounter.WithValues(version.Version, version.Revision).Inc()
	metrics.Register(ns)
}
