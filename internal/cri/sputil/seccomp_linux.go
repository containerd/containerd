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
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/containerd/containerd/v2/core/containers"
	"github.com/containerd/errdefs"
	"github.com/containerd/log"
	specs "github.com/opencontainers/runtime-spec/specs-go"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	merger "sigs.k8s.io/security-profiles-merger/seccomp"

	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	"github.com/containerd/containerd/v2/contrib/seccomp"
	"github.com/containerd/containerd/v2/pkg/oci"
)

const (
	maxMergeDiffLogBytes = 4096
	mergeDiffTruncation  = " ... [diff truncated]"
)

func truncateMergeDiff(diff string) string {
	if len(diff) <= maxMergeDiffLogBytes {
		return diff
	}
	limit := maxMergeDiffLogBytes - len(mergeDiffTruncation)
	for limit > 0 && !utf8.ValidString(diff[:limit]) {
		limit--
	}
	return diff[:limit] + mergeDiffTruncation
}

func GenerateSeccompSecurityProfile(profilePath string, unsetProfilePath string) (*runtime.SecurityProfile, error) {
	if profilePath != "" {
		return generateSecurityProfile(profilePath)
	}
	if unsetProfilePath != "" {
		return generateSecurityProfile(unsetProfilePath)
	}
	return nil, nil
}

// GenerateSeccompSpecOpts generates containerd SpecOpts for seccomp.
func GenerateSeccompSpecOpts(sp *runtime.SecurityProfile, privileged, seccompEnabled bool, loaders ...ProfileLoader) (oci.SpecOpts, error) {
	if err := ValidateOCIFields(sp, true, privileged); err != nil {
		return nil, err
	}
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
	case runtime.SecurityProfile_OCI:
		if len(loaders) != 1 || loaders[0] == nil {
			return nil, errors.New("security profile loader is unavailable")
		}
		return func(ctx context.Context, _ oci.Client, _ *containers.Container, s *specs.Spec) error {
			artifact, err := loaders[0](sp.OciRef)
			if err != nil {
				if errdefs.IsNotFound(err) {
					return status.Errorf(codes.InvalidArgument, "load OCI seccomp profile %q: %v", sp.OciRef, err)
				}
				return fmt.Errorf("load OCI seccomp profile: %w", err)
			}
			inputs := []*specs.LinuxSeccomp{seccomp.DefaultProfile(s)}
			if base := sp.BaseProfile; base != nil && base.Type == runtime.SecurityProfileBase_Localhost {
				data, err := os.ReadFile(base.LocalhostRef)
				if err != nil {
					return status.Errorf(codes.InvalidArgument, "load seccomp base profile: %v", err)
				}
				var local specs.LinuxSeccomp
				if err := json.Unmarshal(data, &local); err != nil {
					return status.Errorf(codes.InvalidArgument, "decode seccomp base profile: %v", err)
				}
				inputs = append(inputs, &local)
			}
			inputs = append(inputs, artifact)
			effective, err := merger.Intersect(inputs...)
			if err != nil {
				return fmt.Errorf("merge OCI seccomp profile: %w", err)
			}
			diff, err := merger.Diff(artifact, effective)
			if err != nil {
				return fmt.Errorf("compare OCI seccomp profile: %w", err)
			}
			if !diff.Equal {
				constrainedMerges.Inc()
				formattedDiff := truncateMergeDiff(merger.FormatDiff(diff))
				log.G(ctx).WithFields(log.Fields{"profile": sp.OciRef, "message_id": "SecurityProfileMergeConstrained", "runtime_baseline": "RuntimeDefault", "base_profile": sp.BaseProfile}).Warnf("SecurityProfileMergeConstrained: %s", formattedDiff)
			}
			if s.Linux == nil {
				s.Linux = &specs.Linux{}
			}
			s.Linux.Seccomp = effective
			return nil
		}, nil
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
