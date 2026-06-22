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
	"fmt"
	"strconv"
	"strings"

	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	"github.com/containerd/containerd/v2/core/snapshots"
	"github.com/containerd/containerd/v2/pkg/oci"

	customopts "github.com/containerd/containerd/v2/internal/cri/opts"
	"github.com/containerd/containerd/v2/internal/cri/sputil"
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

	switch securityContext.GetSupplementalGroupsPolicy() {
	case runtime.SupplementalGroupsPolicy_Merge:
		// merging group defined in /etc/passwd
		// and SupplementalGroups defined in security context
		specOpts = append(specOpts,
			customopts.WithAdditionalGIDs(userstr),
			customopts.WithSupplementalGroups(securityContext.GetSupplementalGroups()),
		)
	case runtime.SupplementalGroupsPolicy_Strict:
		// no merging group defined in /etc/passwd
		specOpts = append(specOpts,
			customopts.WithSupplementalGroups(securityContext.GetSupplementalGroups()),
		)
	default:
		return nil, fmt.Errorf("not implemented in this containerd release: SupplementalGroupsPolicy=%d", securityContext.GetSupplementalGroupsPolicy())
	}

	asp := securityContext.GetApparmor()
	if asp == nil {
		asp, err = sputil.GenerateApparmorSecurityProfile(securityContext.GetApparmorProfile()) //nolint:staticcheck // Deprecated but we don't want to remove yet
		if err != nil {
			return nil, fmt.Errorf("failed to generate apparmor spec opts: %w", err)
		}
	}
	apparmorSpecOpts, err := sputil.GenerateApparmorSpecOpts(
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
		ssp, err = sputil.GenerateSeccompSecurityProfile(
			securityContext.GetSeccompProfilePath(), //nolint:staticcheck // Deprecated but we don't want to remove yet
			c.config.UnsetSeccompProfile)
		if err != nil {
			return nil, fmt.Errorf("failed to generate seccomp spec opts: %w", err)
		}
	}
	seccompSpecOpts, err := sputil.GenerateSeccompSpecOpts(
		ssp,
		securityContext.GetPrivileged(),
		c.seccompEnabled())
	if err != nil {
		return nil, fmt.Errorf("failed to generate seccomp spec opts: %w", err)
	}
	if seccompSpecOpts != nil {
		specOpts = append(specOpts, seccompSpecOpts)
	}
	if c.config.EnableCDI == nil || *c.config.EnableCDI {
		specOpts = append(specOpts, customopts.WithCDI(config.Annotations, config.CDIDevices))
	} else {
		if len(config.CDIDevices) > 0 {
			names, sep := "", ""
			for _, dev := range config.CDIDevices {
				names += sep + dev.Name
				sep = ", "
			}
			return nil, fmt.Errorf("CDI devices (%s) requested but CDI support is explicitly disabled", names)
		}
	}

	return specOpts, nil
}

// snapshotterOpts returns any Linux specific snapshotter options for the rootfs snapshot
func snapshotterOpts(config *runtime.ContainerConfig) ([]snapshots.Opt, error) {
	nsOpts := config.GetLinux().GetSecurityContext().GetNamespaceOptions()
	return snapshotterRemapOpts(nsOpts)
}
