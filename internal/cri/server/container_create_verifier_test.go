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

package server

import (
	"context"
	"errors"
	"testing"

	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	"github.com/containerd/containerd/v2/internal/cri/annotations"
	"github.com/containerd/containerd/v2/pkg/imageverifier"
)

// fakeContextVerifier implements imageverifier.ContextImageVerifier and records
// the options it was called with.
type fakeContextVerifier struct {
	judgement  *imageverifier.Judgement
	err        error
	calledWith *imageverifier.VerifyOptions
}

func (f *fakeContextVerifier) VerifyImage(ctx context.Context, name string, desc imagespec.Descriptor) (*imageverifier.Judgement, error) {
	return &imageverifier.Judgement{OK: true}, nil
}

func (f *fakeContextVerifier) VerifyImageContext(ctx context.Context, name string, desc imagespec.Descriptor, opts imageverifier.VerifyOptions) (*imageverifier.Judgement, error) {
	o := opts
	f.calledWith = &o
	return f.judgement, f.err
}

// fakeLegacyVerifier implements only imageverifier.ImageVerifier and records
// whether it was invoked, so we can assert it is skipped at run time.
type fakeLegacyVerifier struct {
	called bool
}

func (f *fakeLegacyVerifier) VerifyImage(ctx context.Context, name string, desc imagespec.Descriptor) (*imageverifier.Judgement, error) {
	f.called = true
	return &imageverifier.Judgement{OK: true}, nil
}

func runTestSandboxConfig() *runtime.PodSandboxConfig {
	return &runtime.PodSandboxConfig{
		Metadata: &runtime.PodSandboxMetadata{
			Name:      "test-pod",
			Uid:       "00000000-0000-0000-0000-000000000000",
			Namespace: "test-namespace",
		},
		Labels:      map[string]string{"sandbox-label": "sval"},
		Annotations: map[string]string{"sandbox-annotation": "saval"},
	}
}

func runTestContainerConfig() *runtime.ContainerConfig {
	return &runtime.ContainerConfig{
		Metadata:    &runtime.ContainerMetadata{Name: "test-ctr"},
		Labels:      map[string]string{"container-label": "cval"},
		Annotations: map[string]string{"container-annotation": "caval"},
	}
}

func TestVerifyImageForRun(t *testing.T) {
	const (
		imageName = "registry.example.com/image:abc"
		digest    = "sha256:98ea6e4f216f2fb4b69fff9b3a44842c38686ca685f3f55dc48c5d3fb1107be4"
		sandboxID = "sandbox-id-123"
		ctrName   = "test-ctr"
	)
	desc := imagespec.Descriptor{Digest: digest}
	ctx := context.Background()

	verify := func(c *criService) error {
		return c.verifyImageForRun(ctx, imageName, desc, sandboxID, ctrName, runTestContainerConfig(), runTestSandboxConfig())
	}

	t.Run("no verifiers configured is a no-op", func(t *testing.T) {
		c := newTestCRIService()
		assert.NoError(t, verify(c))
	})

	t.Run("context verifier allows run and receives pass-through annotations", func(t *testing.T) {
		vf := &fakeContextVerifier{judgement: &imageverifier.Judgement{OK: true}}
		c := newTestCRIService(func(s *criService) {
			s.imageVerifiers = map[string]imageverifier.ImageVerifier{"ok": vf}
		})

		require.NoError(t, verify(c))
		require.NotNil(t, vf.calledWith)

		opts := vf.calledWith
		assert.Equal(t, imageverifier.OperationRun, opts.Operation)

		// Request annotations (sandbox + container) are passed through.
		assert.Equal(t, "saval", opts.Annotations["sandbox-annotation"])
		assert.Equal(t, "caval", opts.Annotations["container-annotation"])
		// Well-known CRI identity annotations are present (reused from
		// DefaultCRIAnnotations), carrying pod identity via standard keys.
		assert.Equal(t, "test-namespace", opts.Annotations[annotations.SandboxNamespace])
		assert.Equal(t, "test-pod", opts.Annotations[annotations.SandboxName])
		assert.Equal(t, "00000000-0000-0000-0000-000000000000", opts.Annotations[annotations.SandboxUID])
		assert.Equal(t, sandboxID, opts.Annotations[annotations.SandboxID])
		assert.Equal(t, ctrName, opts.Annotations[annotations.ContainerName])
		assert.Equal(t, imageName, opts.Annotations[annotations.ImageName])
	})

	t.Run("context verifier rejection blocks run", func(t *testing.T) {
		vf := &fakeContextVerifier{judgement: &imageverifier.Judgement{OK: false, Reason: "not signed"}}
		c := newTestCRIService(func(s *criService) {
			s.imageVerifiers = map[string]imageverifier.ImageVerifier{"deny": vf}
		})

		err := verify(c)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not signed")
	})

	t.Run("context verifier error blocks run", func(t *testing.T) {
		vf := &fakeContextVerifier{err: errors.New("boom")}
		c := newTestCRIService(func(s *criService) {
			s.imageVerifiers = map[string]imageverifier.ImageVerifier{"err": vf}
		})

		err := verify(c)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "boom")
	})

	t.Run("legacy verifier is skipped at run time", func(t *testing.T) {
		legacy := &fakeLegacyVerifier{}
		c := newTestCRIService(func(s *criService) {
			s.imageVerifiers = map[string]imageverifier.ImageVerifier{"legacy": legacy}
		})

		assert.NoError(t, verify(c))
		assert.False(t, legacy.called, "legacy ImageVerifier must not be invoked at run time")
	})

	t.Run("one rejecting verifier among several blocks run", func(t *testing.T) {
		ok := &fakeContextVerifier{judgement: &imageverifier.Judgement{OK: true}}
		deny := &fakeContextVerifier{judgement: &imageverifier.Judgement{OK: false, Reason: "not signed"}}
		c := newTestCRIService(func(s *criService) {
			s.imageVerifiers = map[string]imageverifier.ImageVerifier{"ok": ok, "deny": deny}
		})

		err := verify(c)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not signed")
	})

	t.Run("legacy and context verifiers together: only context runs", func(t *testing.T) {
		legacy := &fakeLegacyVerifier{}
		cv := &fakeContextVerifier{judgement: &imageverifier.Judgement{OK: true}}
		c := newTestCRIService(func(s *criService) {
			s.imageVerifiers = map[string]imageverifier.ImageVerifier{"legacy": legacy, "ctx": cv}
		})

		require.NoError(t, verify(c))
		assert.False(t, legacy.called, "legacy ImageVerifier must not be invoked at run time")
		assert.NotNil(t, cv.calledWith, "context verifier must be invoked at run time")
	})
}

func TestRunVerifierAnnotations(t *testing.T) {
	const (
		imageName = "registry.example.com/image:abc"
		sandboxID = "sandbox-id-123"
		ctrName   = "test-ctr"
	)

	t.Run("container annotation overrides sandbox annotation", func(t *testing.T) {
		sandboxConfig := runTestSandboxConfig()
		sandboxConfig.Annotations["shared"] = "from-sandbox"
		containerConfig := runTestContainerConfig()
		containerConfig.Annotations["shared"] = "from-container"

		ann := runVerifierAnnotations(sandboxID, ctrName, imageName, containerConfig, sandboxConfig)
		assert.Equal(t, "from-container", ann["shared"], "container annotation should override sandbox annotation")
	})

	t.Run("well-known CRI keys override spoofed request annotations", func(t *testing.T) {
		sandboxConfig := runTestSandboxConfig()
		containerConfig := runTestContainerConfig()
		// A workload tries to spoof its identity via request annotations.
		sandboxConfig.Annotations[annotations.SandboxNamespace] = "spoofed-namespace"
		containerConfig.Annotations[annotations.ImageName] = "spoofed-image"

		ann := runVerifierAnnotations(sandboxID, ctrName, imageName, containerConfig, sandboxConfig)
		assert.Equal(t, "test-namespace", ann[annotations.SandboxNamespace], "authoritative sandbox namespace must win over spoofed value")
		assert.Equal(t, imageName, ann[annotations.ImageName], "authoritative image name must win over spoofed value")
	})

	t.Run("nil sandbox metadata and annotations do not panic", func(t *testing.T) {
		sandboxConfig := &runtime.PodSandboxConfig{}  // nil Metadata, nil Annotations
		containerConfig := &runtime.ContainerConfig{} // nil Annotations

		ann := runVerifierAnnotations(sandboxID, ctrName, imageName, containerConfig, sandboxConfig)
		// Identity keys are still assembled (with empty values where metadata is absent).
		assert.Equal(t, imageName, ann[annotations.ImageName])
		assert.Equal(t, ctrName, ann[annotations.ContainerName])
		assert.Empty(t, ann[annotations.SandboxNamespace])
	})
}
