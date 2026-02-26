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
	"fmt"

	sdkTrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

const (
	samplerNameBased       = "namebased"
	samplerParentBasedName = "parentbased_name"
)

type NameSampler struct {
	// allow is a set of names that should be sampled.
	// Uses a map of empty structs for O(1) lookups and no memory overhead.
	allow map[string]struct{}
}

// NameBased returns a Sampler that samples every span having a certain name.
// It should be used in conjunction with the ParentBased sampler so that the child spans are also sampled.
func NameBased(allowedNames []string) NameSampler {
	allowedNamesMap := make(map[string]struct{}, len(allowedNames))
	for _, name := range allowedNames {
		allowedNamesMap[name] = struct{}{}
	}
	return NameSampler{
		allow: allowedNamesMap,
	}
}

func (ns NameSampler) ShouldSample(parameters sdkTrace.SamplingParameters) sdkTrace.SamplingResult {
	psc := trace.SpanContextFromContext(parameters.ParentContext)

	if _, ok := ns.allow[parameters.Name]; ok {
		return sdkTrace.SamplingResult{
			Decision:   sdkTrace.RecordAndSample,
			Tracestate: psc.TraceState(),
		}
	}

	return sdkTrace.SamplingResult{
		Decision:   sdkTrace.Drop,
		Tracestate: psc.TraceState(),
	}
}

func (ns NameSampler) Description() string {
	return fmt.Sprintf("NameBased:{%v}", ns.allow)
}
