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

type ImageVerifier interface {
	VerifyImage(ctx context.Context, name string, desc ocispec.Descriptor) (*Judgement, error)
}

// Operation identifies the lifecycle phase a verifier is invoked from.
type Operation string

const (
	// OperationPull is verification during an image pull.
	OperationPull Operation = "pull"
	// OperationRun is verification when a container is created from a pulled image.
	OperationRun Operation = "run"
)

// VerifyOptions carries optional, operation-scoped context to verifiers.
// Annotations is a plain string map so this package stays caller-agnostic; any
// Kubernetes meaning lives in well-known keys (e.g. io.kubernetes.cri.*) and is
// not validated by containerd.
type VerifyOptions struct {
	Operation   Operation
	Annotations map[string]string
}

// ContextImageVerifier is an optional interface a verifier may implement to
// receive operation-scoped context. Callers type-assert for it and fall back to
// VerifyImage.
type ContextImageVerifier interface {
	ImageVerifier
	VerifyImageContext(ctx context.Context, name string, desc ocispec.Descriptor, opts VerifyOptions) (*Judgement, error)
}

// VerifierInput is the stdin payload for the
// images.MediaTypeContainerd1ImageVerificationInput media type.
type VerifierInput struct {
	Descriptor  *ocispec.Descriptor `json:"descriptor,omitempty"`
	Operation   Operation           `json:"operation,omitempty"`
	Annotations map[string]string   `json:"annotations,omitempty"`
}

type Judgement struct {
	OK     bool
	Reason string
}
