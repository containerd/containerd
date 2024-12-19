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
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	"github.com/containerd/containerd/v2/contrib/apparmor"
	"github.com/containerd/containerd/v2/pkg/oci"
)

func GenerateApparmorSecurityProfile(profilePath string) (*runtime.SecurityProfile, error) {
	if profilePath != "" {
		return generateSecurityProfile(profilePath)
	}
	return nil, nil
}

// generateApparmorSpecOpts generates containerd SpecOpts for apparmor.
func GenerateApparmorSpecOpts(sp *runtime.SecurityProfile, privileged, apparmorEnabled bool) (oci.SpecOpts, error) {
	if !apparmorEnabled {
		// Should fail loudly if user try to specify apparmor profile
		// but we don't support it.
		if sp != nil {
			if sp.ProfileType != runtime.SecurityProfile_Unconfined {
				return nil, errors.New("apparmor is not supported")
			}
		}
		return nil, nil
	}

	if sp == nil {
		// Based on kubernetes#51746, default apparmor profile should be applied
		// for when apparmor is not specified.
		sp, _ = generateSecurityProfile("")
	}

	if sp.ProfileType != runtime.SecurityProfile_Localhost && sp.LocalhostRef != "" {
		return nil, errors.New("apparmor config invalid LocalhostRef must only be set if ProfileType is Localhost")
	}

	switch sp.ProfileType {
	case runtime.SecurityProfile_Unconfined:
		// Do not set apparmor profile.
		return nil, nil
	case runtime.SecurityProfile_RuntimeDefault:
		if privileged {
			// Do not set apparmor profile when container is privileged
			return nil, nil
		}
		// TODO (mikebrow): delete created apparmor default profile
		return apparmor.WithDefaultProfile(appArmorDefaultProfileName), nil
	case runtime.SecurityProfile_Localhost:
		// trimming the localhost/ prefix just in case even through it should not
		// be necessary with the new SecurityProfile struct
		appArmorProfile := strings.TrimPrefix(sp.LocalhostRef, profileNamePrefix)
		if profileExists, err := appArmorProfileExists(appArmorProfile); !profileExists {
			if err != nil {
				return nil, fmt.Errorf("failed to generate apparmor spec opts: %w", err)
			}
			return nil, fmt.Errorf("apparmor profile not found %s", appArmorProfile)
		}
		return apparmor.WithProfile(appArmorProfile), nil
	default:
		return nil, errors.New("apparmor unknown ProfileType")
	}
}

// appArmorProfileExists scans apparmor/profiles for the requested profile
func appArmorProfileExists(profile string) (bool, error) {
	if profile == "" {
		return false, errors.New("nil apparmor profile is not supported")
	}
	profiles, err := os.Open("/sys/kernel/security/apparmor/profiles")
	if err != nil {
		return false, err
	}
	defer profiles.Close()

	rbuff := bufio.NewReader(profiles)
	for {
		line, err := rbuff.ReadString('\n')
		switch err {
		case nil:
			if strings.HasPrefix(line, profile+" (") {
				return true, nil
			}
		case io.EOF:
			return false, nil
		default:
			return false, err
		}
	}
}
