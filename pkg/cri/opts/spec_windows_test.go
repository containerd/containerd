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

package opts

import (
	"fmt"
	"strings"
	"testing"

	osinterface "github.com/containerd/containerd/pkg/os"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

func TestDriveMounts(t *testing.T) {
	tests := []struct {
		mnt                   *runtime.Mount
		expectedContainerPath string
		expectedError         error
	}{
		{&runtime.Mount{HostPath: `C:\`, ContainerPath: `D:\foo`}, `D:\foo`, nil},
		{&runtime.Mount{HostPath: `C:\`, ContainerPath: `D:\`}, `D:\`, nil},
		{&runtime.Mount{HostPath: `C:\`, ContainerPath: `D:`}, `D:`, nil},
		{&runtime.Mount{HostPath: `\\.\pipe\a_fake_pipe_name_that_shouldnt_exist`, ContainerPath: `\\.\pipe\foo`}, `\\.\pipe\foo`, nil},
		// If `C:\` is passed as container path it should continue and forward that to HCS and fail
		// to align with docker's behavior.
		{&runtime.Mount{HostPath: `C:\`, ContainerPath: `C:\`}, `C:\`, nil},

		// If `C:` is passed we can detect and fail immediately.
		{&runtime.Mount{HostPath: `C:\`, ContainerPath: `C:`}, ``, fmt.Errorf("destination path can not be C drive")},
	}
	var realOS osinterface.RealOS
	for _, test := range tests {
		parsedMount, err := parseMount(realOS, test.mnt)
		if err != nil && !strings.EqualFold(err.Error(), test.expectedError.Error()) {
			t.Fatalf("expected err: %s, got %s instead", test.expectedError, err)
		} else if err == nil && test.expectedContainerPath != parsedMount.Destination {
			t.Fatalf("expected container path: %s, got %s instead", test.expectedContainerPath, parsedMount.Destination)
		}
	}
}
