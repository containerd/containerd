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
	"testing"

	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/stretchr/testify/assert"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	"github.com/containerd/containerd/v2/contrib/seccomp"
	"github.com/containerd/containerd/v2/internal/cri/util"
	"github.com/containerd/containerd/v2/pkg/oci"
)

func TestGenerateSeccompSecurityProfileSpecOpts(t *testing.T) {
	for _, test := range []struct {
		desc           string
		profile        string
		privileged     bool
		disable        bool
		specOpts       oci.SpecOpts
		expectErr      bool
		defaultProfile string
		sp             *runtime.SecurityProfile
	}{
		{
			desc:      "should return error if seccomp is specified when seccomp is not supported",
			profile:   runtimeDefault,
			disable:   true,
			expectErr: true,
		},
		{
			desc:    "should not return error if seccomp is not specified when seccomp is not supported",
			profile: "",
			disable: true,
		},
		{
			desc:    "should not return error if seccomp is unconfined when seccomp is not supported",
			profile: unconfinedProfile,
			disable: true,
		},
		{
			desc:       "should not set seccomp when privileged is true",
			profile:    seccompDefaultProfile,
			privileged: true,
		},
		{
			desc:    "should not set seccomp when seccomp is unconfined",
			profile: unconfinedProfile,
		},
		{
			desc:    "should not set seccomp when seccomp is not specified",
			profile: "",
		},
		{
			desc:     "should set default seccomp when seccomp is runtime/default",
			profile:  runtimeDefault,
			specOpts: seccomp.WithDefaultProfile(),
		},
		{
			desc:     "should set default seccomp when seccomp is docker/default",
			profile:  dockerDefault,
			specOpts: seccomp.WithDefaultProfile(),
		},
		{
			desc:     "should set specified profile when local profile is specified",
			profile:  profileNamePrefix + "test-profile",
			specOpts: seccomp.WithProfile("test-profile"),
		},
		{
			desc:           "should use default profile when seccomp is empty",
			defaultProfile: profileNamePrefix + "test-profile",
			specOpts:       seccomp.WithProfile("test-profile"),
		},
		{
			desc:           "should fallback to docker/default when seccomp is empty and default is runtime/default",
			defaultProfile: runtimeDefault,
			specOpts:       seccomp.WithDefaultProfile(),
		},
		//-----------------------------------------------
		// now buckets for the SecurityProfile variants
		//-----------------------------------------------
		{
			desc:      "sp should return error if seccomp is specified when seccomp is not supported",
			disable:   true,
			expectErr: true,
			sp: &runtime.SecurityProfile{
				ProfileType: runtime.SecurityProfile_RuntimeDefault,
			},
		},
		{
			desc:    "sp should not return error if seccomp is unconfined when seccomp is not supported",
			disable: true,
			sp: &runtime.SecurityProfile{
				ProfileType: runtime.SecurityProfile_Unconfined,
			},
		},
		{
			desc:       "sp should not set seccomp when privileged is true",
			privileged: true,
			sp: &runtime.SecurityProfile{
				ProfileType: runtime.SecurityProfile_RuntimeDefault,
			},
		},
		{
			desc: "sp should not set seccomp when seccomp is unconfined",
			sp: &runtime.SecurityProfile{
				ProfileType: runtime.SecurityProfile_Unconfined,
			},
		},
		{
			desc: "sp should not set seccomp when seccomp is not specified",
		},
		{
			desc:     "sp should set default seccomp when seccomp is runtime/default",
			specOpts: seccomp.WithDefaultProfile(),
			sp: &runtime.SecurityProfile{
				ProfileType: runtime.SecurityProfile_RuntimeDefault,
			},
		},
		{
			desc:     "sp should set specified profile when local profile is specified",
			specOpts: seccomp.WithProfile("test-profile"),
			sp: &runtime.SecurityProfile{
				ProfileType:  runtime.SecurityProfile_Localhost,
				LocalhostRef: profileNamePrefix + "test-profile",
			},
		},
		{
			desc:     "sp should set specified profile when local profile is specified even without prefix",
			specOpts: seccomp.WithProfile("test-profile"),
			sp: &runtime.SecurityProfile{
				ProfileType:  runtime.SecurityProfile_Localhost,
				LocalhostRef: "test-profile",
			},
		},
		{
			desc:      "sp should return error if specified profile is invalid",
			expectErr: true,
			sp: &runtime.SecurityProfile{
				ProfileType:  runtime.SecurityProfile_RuntimeDefault,
				LocalhostRef: "test-profile",
			},
		},
	} {
		t.Run(test.desc, func(t *testing.T) {
			ssp := test.sp
			csp, err := GenerateSeccompSecurityProfile(
				test.profile,
				test.defaultProfile)
			if err != nil {
				if test.expectErr {
					assert.Error(t, err)
				} else {
					assert.NoError(t, err)
				}
			} else {
				if ssp == nil {
					ssp = csp
				}
				specOpts, err := GenerateSeccompSpecOpts(ssp, test.privileged, !test.disable)
				if test.expectErr {
					assert.Error(t, err)
				} else {
					assert.NoError(t, err)
					if test.specOpts == nil && specOpts == nil {
						return
					}
					if test.specOpts == nil || specOpts == nil {
						t.Fatalf("unexpected nil specOpts, expected nil: %t, actual nil: %t", test.specOpts == nil, specOpts == nil)
					}
					// `specOpts` for seccomp only uses/modifies `*specs.Spec`, not
					// `oci.Client` or `*containers.Container`, so let's construct a
					// `*specs.Spec` and compare if the results are the same.
					expected := runtimespec.Spec{
						Linux: &runtimespec.Linux{},
						Process: &runtimespec.Process{
							Capabilities: &runtimespec.LinuxCapabilities{
								// This is to ensure the test covers logic in
								// `contrib/seccomp.DefaultProfile` as much as possible
								Bounding: []string{
									"CAP_DAC_READ_SEARCH",
									"CAP_SYS_ADMIN",
									"CAP_SYS_BOOT",
									"CAP_SYS_CHROOT",
									"CAP_SYS_MODULE",
									"CAP_SYS_PACCT",
									"CAP_SYS_PTRACE",
									"CAP_SYS_RAWIO",
									"CAP_SYS_TIME",
									"CAP_SYS_TTY_CONFIG",
									"CAP_SYS_NICE",
									"CAP_SYSLOG",
									"CAP_BPF",
									"CAP_PERFMON",
								},
							},
						},
					}
					var actual runtimespec.Spec
					err := util.DeepCopy(&actual, &expected)
					assert.NoError(t, err)

					test.specOpts(context.TODO(), nil, nil, &expected)
					specOpts(context.TODO(), nil, nil, &actual)
					assert.Equal(t, expected, actual)
				}
			}
		})
	}
}
