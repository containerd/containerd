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

package transfer

import (
	"github.com/docker/go-metrics"

	ctransfer "github.com/containerd/containerd/v2/core/transfer"
)

var resolverStats *ctransfer.ImageResolverStats

func init() {
	ns := metrics.NewNamespace("containerd", "transfer", nil)
	resolverStats = &ctransfer.ImageResolverStats{
		DescriptorURLRequests: ns.NewCounter("descriptor_url_requests", "Number of requests made to URLs from image descriptors"),
	}
	metrics.Register(ns)
}
