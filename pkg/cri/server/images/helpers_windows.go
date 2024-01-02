//go:build windows

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

package images

import (
	"fmt"

	runhcsoptions "github.com/Microsoft/hcsshim/cmd/containerd-shim-runhcs-v1/options"
	criconfig "github.com/containerd/containerd/v2/pkg/cri/config"
	"github.com/containerd/containerd/v2/platforms"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
)

func isRuntimeHandlerVMBased(runtimeOpts interface{}) bool {
	rhcso, ok := runtimeOpts.(*runhcsoptions.Options)
	if ok {
		if rhcso == nil {
			return false
		}
		return rhcso.SandboxIsolation == 1 // hyperV Isolated
	}
	return false
}

func GetPlatformForRuntimeHandler(ociRuntime criconfig.Runtime, runtimeHandler string) (specs.Platform, error) {
	runtimeOpts, err := criconfig.GenerateRuntimeOptions(ociRuntime)
	if err != nil {
		return specs.Platform{}, fmt.Errorf("failed to get runtime options for runtime: %v", runtimeHandler)
	}

	if isRuntimeHandlerVMBased(runtimeOpts) {
		// If windows OS, check if hostOSVersion and Platform.OsVersion are compatible.
		// That is, are the host and UVM compatible based on the msdocs compat matricx (mentioned in 4126  KEP).
		isCompat := platforms.AreWindowsHostAndGuestHyperVCompatible(platforms.DefaultSpec(), ociRuntime.Platform)
		if !isCompat {
			return specs.Platform{}, fmt.Errorf("incompatible host and guest OSVersions")
		}

		return platforms.Normalize(ociRuntime.Platform), nil
	}
	return specs.Platform{}, fmt.Errorf("runtime.Platform cannot override the host platform for process isolation")
}
