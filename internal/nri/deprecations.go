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

package nri

import (
	"context"

	"github.com/containerd/containerd/v2/pkg/deprecation"
	"github.com/containerd/containerd/v2/plugins/services/warning"
	"github.com/containerd/log"

	nri "github.com/containerd/nri/pkg/adaptation"
)

type recorder struct {
	ws warning.Service
}

func (r *recorder) PluginWarning(ctx context.Context, d nri.Deprecation, plugin, details string) {
	switch d {
	case nri.DeprecatedStateChange:
		r.ws.Emit(ctx, deprecation.NRIPluginInterface)
		msg, _ := deprecation.Message(deprecation.NRIPluginInterface)
		log.G(ctx).WithFields(log.Fields{
			"deprecated": "StateChange",
			"plugin":     plugin,
			"details":    details,
		}).Warn(msg)
	default:
		log.G(ctx).WithFields(log.Fields{
			"deprecated": d.String(),
			"plugin":     plugin,
			"details":    details,
		}).Warnf("unknown NRI deprecation (%d)", d)
	}
}
