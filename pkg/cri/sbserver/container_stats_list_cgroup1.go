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

package sbserver

import (
	cg1 "github.com/containerd/cgroups/v3/cgroup1/stats"
	"github.com/containerd/containerd/api/types"
	"github.com/containerd/typeurl/v2"
)

func allocMetricCgroup1(stats *types.Metric) interface{} {
	if typeurl.Is(stats.Data, (*cg1.Metrics)(nil)) {
		return &cg1.Metrics{}
	}
	return nil
}
