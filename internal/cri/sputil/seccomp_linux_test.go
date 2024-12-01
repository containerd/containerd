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
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	"github.com/containerd/containerd/v2/contrib/seccomp"
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
		test := test
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
				assert.Equal(t,
					reflect.ValueOf(test.specOpts).Pointer(),
					reflect.ValueOf(specOpts).Pointer())
				if test.expectErr {
					assert.Error(t, err)
				} else {
					assert.NoError(t, err)
				}
			}
		})
	}
}
