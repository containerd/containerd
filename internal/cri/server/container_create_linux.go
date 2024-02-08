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
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	"github.com/containerd/containerd/v2/contrib/apparmor"
	"github.com/containerd/containerd/v2/contrib/seccomp"
	"github.com/containerd/containerd/v2/core/snapshots"
	"github.com/containerd/containerd/v2/pkg/oci"

	customopts "github.com/containerd/containerd/v2/internal/cri/opts"
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

func (c *criService) containerSpecOpts(config *runtime.ContainerConfig, imageConfig *imagespec.ImageConfig) ([]oci.SpecOpts, error) {
	var (
		specOpts []oci.SpecOpts
		err      error
	)
	securityContext := config.GetLinux().GetSecurityContext()
	userstr := "0" // runtime default
	if securityContext.GetRunAsUsername() != "" {
		userstr = securityContext.GetRunAsUsername()
	} else if securityContext.GetRunAsUser() != nil {
		userstr = strconv.FormatInt(securityContext.GetRunAsUser().GetValue(), 10)
	} else if imageConfig.User != "" {
		userstr, _, _ = strings.Cut(imageConfig.User, ":")
	}
	specOpts = append(specOpts, customopts.WithAdditionalGIDs(userstr),
		customopts.WithSupplementalGroups(securityContext.GetSupplementalGroups()))

	asp := securityContext.GetApparmor()
	if asp == nil {
		asp, err = generateApparmorSecurityProfile(securityContext.GetApparmorProfile()) //nolint:staticcheck // Deprecated but we don't want to remove yet
		if err != nil {
			return nil, fmt.Errorf("failed to generate apparmor spec opts: %w", err)
		}
	}
	apparmorSpecOpts, err := generateApparmorSpecOpts(
		asp,
		securityContext.GetPrivileged(),
		c.apparmorEnabled())
	if err != nil {
		return nil, fmt.Errorf("failed to generate apparmor spec opts: %w", err)
	}
	if apparmorSpecOpts != nil {
		specOpts = append(specOpts, apparmorSpecOpts)
	}

	ssp := securityContext.GetSeccomp()
	if ssp == nil {
		ssp, err = generateSeccompSecurityProfile(
			securityContext.GetSeccompProfilePath(), //nolint:staticcheck // Deprecated but we don't want to remove yet
			c.config.UnsetSeccompProfile)
		if err != nil {
			return nil, fmt.Errorf("failed to generate seccomp spec opts: %w", err)
		}
	}
	seccompSpecOpts, err := c.generateSeccompSpecOpts(
		ssp,
		securityContext.GetPrivileged(),
		c.seccompEnabled())
	if err != nil {
		return nil, fmt.Errorf("failed to generate seccomp spec opts: %w", err)
	}
	if seccompSpecOpts != nil {
		specOpts = append(specOpts, seccompSpecOpts)
	}
	if c.config.EnableCDI {
		specOpts = append(specOpts, customopts.WithCDI(config.Annotations, config.CDIDevices))
	}
	return specOpts, nil
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
func generateApparmorSecurityProfile(profilePath string) (*runtime.SecurityProfile, error) {
	if profilePath != "" {
		return generateSecurityProfile(profilePath)
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

// generateSeccompSpecOpts generates containerd SpecOpts for seccomp.
func (c *criService) generateSeccompSpecOpts(sp *runtime.SecurityProfile, privileged, seccompEnabled bool) (oci.SpecOpts, error) {
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

// generateApparmorSpecOpts generates containerd SpecOpts for apparmor.
func generateApparmorSpecOpts(sp *runtime.SecurityProfile, privileged, apparmorEnabled bool) (oci.SpecOpts, error) {
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

// snapshotterOpts returns any Linux specific snapshotter options for the rootfs snapshot
func snapshotterOpts(config *runtime.ContainerConfig) ([]snapshots.Opt, error) {
	nsOpts := config.GetLinux().GetSecurityContext().GetNamespaceOptions()
	return snapshotterRemapOpts(nsOpts)
}
