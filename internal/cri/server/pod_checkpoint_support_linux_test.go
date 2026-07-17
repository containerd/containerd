//go:build linux

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
	"errors"
	"testing"

	criconfig "github.com/containerd/containerd/v2/internal/cri/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

func TestRestorePodSelectsCustomControllerWithoutCRIU(t *testing.T) {
	cri := newTestCRIService()
	cri.podCheckpointSupportCheck = nil
	cri.shimPath = t.TempDir()
	cri.config.Runtimes = map[string]criconfig.Runtime{
		"vm-runtime": {Sandboxer: "custom-controller"},
	}
	controllerErr := errors.New("selected custom controller")
	service := &checkpointSandboxService{err: controllerErr}
	cri.sandboxService = service

	_, err := cri.RestorePod(checkpointAPIContext(t), restoreTransactionRequest())
	require.ErrorIs(t, err, controllerErr)
	assert.Equal(t, "custom-controller", service.sandboxer)
}

func TestPodCheckpointAPIsDisabledByConfiguration(t *testing.T) {
	cri := newTestCRIService()
	cri.podCheckpointSupportCheck = nil
	enabled := false
	cri.config.EnableCRIU = &enabled
	cri.shimPath = t.TempDir()

	_, err := cri.CheckpointPod(checkpointAPIContext(t), &runtime.CheckpointPodRequest{PodSandboxId: "sandbox"})
	require.ErrorContains(t, err, "criu support is disabled by configuration")
	_, err = cri.RestorePod(checkpointAPIContext(t), restoreTransactionRequest())
	require.ErrorContains(t, err, "criu support is disabled by configuration")
}
