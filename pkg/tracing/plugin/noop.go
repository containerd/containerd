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

package plugin

import (
	"context"

	"github.com/containerd/containerd/v2/plugins"
	"github.com/containerd/plugin"
	"github.com/containerd/plugin/registry"
	"go.opentelemetry.io/otel/sdk/trace"
)

// NoopProcessor is a dummy tracing processor to satisfy plugin dependency
type NoopProcessor struct{}

// OnStart implements trace.SpanProcessor interface
func (n *NoopProcessor) OnStart(parent context.Context, s trace.ReadWriteSpan) {}

// OnEnd implements trace.SpanProcessor interface
func (n *NoopProcessor) OnEnd(s trace.ReadOnlySpan) {}

// ForceFlush implements trace.SpanProcessor interface
func (n *NoopProcessor) ForceFlush(ctx context.Context) error {
	return nil
}

// Shutdown implements trace.SpanProcessor interface
func (n *NoopProcessor) Shutdown(ctx context.Context) error {
	return nil
}

func init() {
	registry.Register(&plugin.Registration{
		ID:   "noop",
		Type: plugins.TracingProcessorPlugin,
		InitFn: func(ic *plugin.InitContext) (interface{}, error) {
			return &NoopProcessor{}, nil
		},
	})
}
