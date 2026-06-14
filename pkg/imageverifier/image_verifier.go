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

package imageverifier

import (
	"context"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// This structure provides `application/vnd.containerd.image-verifier.input.v1+json` mediatype
// when marshalled to JSON.
type PodMetadataInput struct {
	RuntimeConfig *RuntimeConfig      `json:"runtime_config,omitempty"`
	Descriptor    *ocispec.Descriptor `json:"descriptor,omitempty"`
}

// RuntimeConfig is the simplified sandbox information passed to verifier binaries.
type RuntimeConfig struct {
	Metadata    *Metadata         `json:"metadata,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

// Metadata contains the pod metadata information.
type Metadata struct {
	Name      string `json:"name,omitempty"`
	UID       string `json:"uid,omitempty"`
	Namespace string `json:"namespace,omitempty"`
}

type ImageVerifier interface {
	VerifyImage(ctx context.Context, name string, desc ocispec.Descriptor, runtimeConfig *RuntimeConfig) (*Judgement, error)
}

type Judgement struct {
	OK     bool
	Reason string
}
