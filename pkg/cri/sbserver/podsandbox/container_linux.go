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

// TODO: these are copied from container_create_linux.go and should be consolidated later.

package podsandbox

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/containerd/containerd/contrib/seccomp"
	"github.com/containerd/containerd/oci"

	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

const (
	// profileNamePrefix is the prefix for loading profiles on a localhost. Eg. AppArmor localhost/profileName.
	profileNamePrefix = "localhost/" // TODO (mikebrow): get localhost/ & runtime/default from CRI kubernetes/kubernetes#51747
	// runtimeDefault indicates that we should use or create a runtime default profile.
	runtimeDefault = "runtime/default"
	// dockerDefault indicates that we should use or create a docker default profile.
	dockerDefault = "docker/default"
	// unconfinedProfile is a string indicating one should run a pod/containerd without a security profile
	unconfinedProfile = "unconfined"
)

// generateSeccompSpecOpts generates containerd SpecOpts for seccomp.
func (c *Controller) generateSeccompSpecOpts(sp *runtime.SecurityProfile, privileged, seccompEnabled bool) (oci.SpecOpts, error) {
	if privileged {
		// Do not set seccomp profile when container is privileged
		return nil, nil
	}
	if !seccompEnabled {
		if sp != nil {
			if sp.ProfileType != runtime.SecurityProfile_Unconfined {
				return nil, errors.New("seccomp is not supported")
			}
		}
		return nil, nil
	}

	if sp == nil {
		return nil, nil
	}

	if sp.ProfileType != runtime.SecurityProfile_Localhost && sp.LocalhostRef != "" {
		return nil, errors.New("seccomp config invalid LocalhostRef must only be set if ProfileType is Localhost")
	}
	switch sp.ProfileType {
	case runtime.SecurityProfile_Unconfined:
		// Do not set seccomp profile.
		return nil, nil
	case runtime.SecurityProfile_RuntimeDefault:
		return seccomp.WithDefaultProfile(), nil
	case runtime.SecurityProfile_Localhost:
		// trimming the localhost/ prefix just in case even though it should not
		// be necessary with the new SecurityProfile struct
		return seccomp.WithProfile(strings.TrimPrefix(sp.LocalhostRef, profileNamePrefix)), nil
	default:
		return nil, errors.New("seccomp unknown ProfileType")
	}
}

func generateSeccompSecurityProfile(profilePath string, unsetProfilePath string) (*runtime.SecurityProfile, error) {
	if profilePath != "" {
		return generateSecurityProfile(profilePath)
	}
	if unsetProfilePath != "" {
		return generateSecurityProfile(unsetProfilePath)
	}
	return nil, nil
}

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

// generateUserString generates valid user string based on OCI Image Spec
// v1.0.0.
//
// CRI defines that the following combinations are valid:
//
// (none) -> ""
// username -> username
// username, uid -> username
// username, uid, gid -> username:gid
// username, gid -> username:gid
// uid -> uid
// uid, gid -> uid:gid
// gid -> error
//
// TODO(random-liu): Add group name support in CRI.
func generateUserString(username string, uid, gid *runtime.Int64Value) (string, error) {
	var userstr, groupstr string
	if uid != nil {
		userstr = strconv.FormatInt(uid.GetValue(), 10)
	}
	if username != "" {
		userstr = username
	}
	if gid != nil {
		groupstr = strconv.FormatInt(gid.GetValue(), 10)
	}
	if userstr == "" {
		if groupstr != "" {
			return "", fmt.Errorf("user group %q is specified without user", groupstr)
		}
		return "", nil
	}
	if groupstr != "" {
		userstr = userstr + ":" + groupstr
	}
	return userstr, nil
}
