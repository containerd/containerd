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
	"errors"
	"path/filepath"
	goruntime "runtime"
	"slices"
	"strings"

	profilestore "github.com/containerd/containerd/v2/internal/cri/store/securityprofile"
	specs "github.com/opencontainers/runtime-spec/specs-go"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

// ProfileLoader reads only profiles previously accepted by PullSecurityProfile.
type ProfileLoader func(string) (*specs.LinuxSeccomp, error)

// ProfileLoaderFrom adapts the CRI image service for spec generation.
func ProfileLoaderFrom(service any) ProfileLoader {
	return func(ref string) (*specs.LinuxSeccomp, error) {
		loader, ok := service.(interface {
			LoadSecurityProfile(string) (*specs.LinuxSeccomp, error)
		})
		if !ok {
			return nil, errors.New("security profile store is unavailable")
		}
		return loader.LoadSecurityProfile(ref)
	}
}

// ValidateOCIFields checks the new fields before legacy privileged/unsupported
// paths can ignore them. Existing profile types keep their prior behavior.
func ValidateOCIFields(profile *runtime.SecurityProfile, seccomp, privileged bool) error {
	if profile == nil {
		return nil
	}
	invalid := func(message string) error { return status.Error(codes.InvalidArgument, message) }
	if profile.ProfileType != runtime.SecurityProfile_OCI {
		if profile.OciRef != "" || profile.BaseProfile != nil {
			return invalid("OCI fields require the OCI profile type")
		}
		return nil
	}
	if !seccomp {
		return invalid("OCI AppArmor profiles are not supported in alpha")
	}
	if privileged {
		return invalid("OCI seccomp profiles are not supported for privileged containers")
	}
	if profile.LocalhostRef != "" {
		return invalid("OCI profile must not set localhost_ref")
	}
	if _, err := profilestore.ReferenceDigest(profile.OciRef); err != nil {
		return invalid("OCI profile requires a digest-pinned oci_ref")
	}
	if base := profile.BaseProfile; base != nil {
		switch base.Type {
		case runtime.SecurityProfileBase_RuntimeDefault:
			if base.LocalhostRef != "" {
				return invalid("RuntimeDefault base must not set localhost_ref")
			}
		case runtime.SecurityProfileBase_Localhost:
			if !filepath.IsAbs(base.LocalhostRef) || hasParentPathComponent(base.LocalhostRef) {
				return invalid("Localhost base requires an absolute localhost_ref without parent-directory components")
			}
		default:
			return invalid("unsupported OCI base profile type")
		}
	}
	return nil
}

func hasParentPathComponent(path string) bool {
	return slices.Contains(strings.Split(filepath.ToSlash(path), "/"), "..")
}

// ValidateSecurityProfiles validates seccomp and AppArmor fields before creating runtime state.
func ValidateSecurityProfiles(seccomp, apparmor *runtime.SecurityProfile, privileged bool) error {
	if goruntime.GOOS != "linux" && (seccomp.GetProfileType() == runtime.SecurityProfile_OCI || apparmor.GetProfileType() == runtime.SecurityProfile_OCI) {
		return status.Error(codes.InvalidArgument, "OCI security profiles require Linux")
	}
	if err := ValidateOCIFields(seccomp, true, privileged); err != nil {
		return err
	}
	return ValidateOCIFields(apparmor, false, privileged)
}
