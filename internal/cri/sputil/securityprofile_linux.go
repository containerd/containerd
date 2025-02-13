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

package sputil

import (
	"fmt"
	"strings"

	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

const (
	// profileNamePrefix is the prefix for loading profiles on a localhost. Eg. AppArmor localhost/profileName.
	profileNamePrefix = "localhost/" // TODO (mikebrow): get localhost/ & runtime/default from CRI kubernetes/kubernetes#51747
	// runtimeDefault indicates that we should use or create a runtime default profile.
	runtimeDefault = "runtime/default"
	// dockerDefault indicates that we should use or create a docker default profile.
	dockerDefault = "docker/default"
	// appArmorDefaultProfileName is name to use when creating a default apparmor profile.
	appArmorDefaultProfileName = "cri-containerd.apparmor.d"
	// unconfinedProfile is a string indicating one should run a pod/containerd without a security profile
	unconfinedProfile = "unconfined"
	// seccompDefaultProfile is the default seccomp profile.
	seccompDefaultProfile = dockerDefault
)

func generateSecurityProfile(profilePath string) (*runtime.SecurityProfile, error) {
	switch profilePath {
	case runtimeDefault, dockerDefault, "":
		return &runtime.SecurityProfile{
			ProfileType: runtime.SecurityProfile_RuntimeDefault,
		}, nil
	case unconfinedProfile:
		return &runtime.SecurityProfile{
			ProfileType: runtime.SecurityProfile_Unconfined,
		}, nil
	default:
		// Require and Trim default profile name prefix
		if !strings.HasPrefix(profilePath, profileNamePrefix) {
			return nil, fmt.Errorf("invalid profile %q", profilePath)
		}
		return &runtime.SecurityProfile{
			ProfileType:  runtime.SecurityProfile_Localhost,
			LocalhostRef: strings.TrimPrefix(profilePath, profileNamePrefix),
		}, nil
	}
}
