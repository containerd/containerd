//go:build linux && shim_tracing

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

package task

import (
	"context"

	"github.com/containerd/ttrpc"
	"go.opentelemetry.io/contrib/propagators/envcar"
	"go.opentelemetry.io/otel"

	runcC "github.com/containerd/go-runc"
)

// UnaryServerInterceptor propagates the trace context of the request to the
// runc invocations made while serving it.
//
// The trace context itself is extracted from the request metadata by the
// otelttrpc interceptor, which pkg/shim always chains first.
//
// runc and the OCI hooks it spawns are separate processes, so the context can
// not be passed over ttrpc. It travels in their environment instead, as
// TRACEPARENT, TRACESTATE and BAGGAGE, see
// https://opentelemetry.io/docs/specs/otel/context/env-carriers/
func (*service) UnaryServerInterceptor() ttrpc.UnaryServerInterceptor {
	return func(ctx context.Context, unmarshal ttrpc.Unmarshaler, info *ttrpc.UnaryServerInfo, method ttrpc.Method) (any, error) {
		var env []string
		carrier := envcar.Carrier{SetEnvFunc: func(key, value string) {
			env = append(env, key+"="+value)
		}}
		otel.GetTextMapPropagator().Inject(ctx, &carrier)

		return method(runcC.WithExtraEnv(ctx, env...), unmarshal)
	}
}
