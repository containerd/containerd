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

	"github.com/containerd/containerd/v2/contrib/apparmor"
	"github.com/containerd/containerd/v2/internal/cri/util"
	"github.com/containerd/containerd/v2/pkg/oci"
)

func TestGenerateApparmorSpecOpts(t *testing.T) {
	for _, test := range []struct {
		desc       string
		profile    string
		privileged bool
		disable    bool
		specOpts   oci.SpecOpts
		expectErr  bool
		sp         *runtime.SecurityProfile
	}{
		{
			desc:      "should return error if apparmor is specified when apparmor is not supported",
			profile:   runtimeDefault,
			disable:   true,
			expectErr: true,
		},
		{
			desc:    "should not return error if apparmor is not specified when apparmor is not supported",
			profile: "",
			disable: true,
		},
		{
			desc:     "should set default apparmor when apparmor is not specified",
			profile:  "",
			specOpts: apparmor.WithDefaultProfile(appArmorDefaultProfileName),
		},
		{
			desc:       "should not apparmor when apparmor is not specified and privileged is true",
			profile:    "",
			privileged: true,
		},
		{
			desc:    "should not return error if apparmor is unconfined when apparmor is not supported",
			profile: unconfinedProfile,
			disable: true,
		},
		{
			desc:    "should not apparmor when apparmor is unconfined",
			profile: unconfinedProfile,
		},
		{
			desc:       "should not apparmor when apparmor is unconfined and privileged is true",
			profile:    unconfinedProfile,
			privileged: true,
		},
		{
			desc:     "should set default apparmor when apparmor is runtime/default",
			profile:  runtimeDefault,
			specOpts: apparmor.WithDefaultProfile(appArmorDefaultProfileName),
		},
		{
			desc:       "should not apparmor when apparmor is default and privileged is true",
			profile:    runtimeDefault,
			privileged: true,
		},
		// TODO (mikebrow) add success with existing defined profile tests
		{
			desc:      "should return error when undefined local profile is specified",
			profile:   profileNamePrefix + "test-profile",
			expectErr: true,
		},
		{
			desc:       "should return error when undefined local profile is specified and privileged is true",
			profile:    profileNamePrefix + "test-profile",
			privileged: true,
			expectErr:  true,
		},
		{
			desc:      "should return error if specified profile is invalid",
			profile:   "test-profile",
			expectErr: true,
		},
		//--------------------------------------
		// buckets for SecurityProfile struct
		//--------------------------------------
		{
			desc:      "sp should return error if apparmor is specified when apparmor is not supported",
			disable:   true,
			expectErr: true,
			sp: &runtime.SecurityProfile{
				ProfileType: runtime.SecurityProfile_RuntimeDefault,
			},
		},
		{
			desc:    "sp should not return error if apparmor is unconfined when apparmor is not supported",
			disable: true,
			sp: &runtime.SecurityProfile{
				ProfileType: runtime.SecurityProfile_Unconfined,
			},
		},
		{
			desc: "sp should not apparmor when apparmor is unconfined",
			sp: &runtime.SecurityProfile{
				ProfileType: runtime.SecurityProfile_Unconfined,
			},
		},
		{
			desc:       "sp should not apparmor when apparmor is unconfined and privileged is true",
			privileged: true,
			sp: &runtime.SecurityProfile{
				ProfileType: runtime.SecurityProfile_Unconfined,
			},
		},
		{
			desc:     "sp should set default apparmor when apparmor is runtime/default",
			specOpts: apparmor.WithDefaultProfile(appArmorDefaultProfileName),
			sp: &runtime.SecurityProfile{
				ProfileType: runtime.SecurityProfile_RuntimeDefault,
			},
		},
		{
			desc:       "sp should not apparmor when apparmor is default and privileged is true",
			privileged: true,
			sp: &runtime.SecurityProfile{
				ProfileType: runtime.SecurityProfile_RuntimeDefault,
			},
		},
		{
			desc:      "sp should return error when undefined local profile is specified",
			expectErr: true,
			sp: &runtime.SecurityProfile{
				ProfileType:  runtime.SecurityProfile_Localhost,
				LocalhostRef: profileNamePrefix + "test-profile",
			},
		},
		{
			desc:      "sp should return error when undefined local profile is specified even without prefix",
			profile:   profileNamePrefix + "test-profile",
			expectErr: true,
			sp: &runtime.SecurityProfile{
				ProfileType:  runtime.SecurityProfile_Localhost,
				LocalhostRef: "test-profile",
			},
		},
		{
			desc:       "sp should return error when undefined local profile is specified and privileged is true",
			privileged: true,
			expectErr:  true,
			sp: &runtime.SecurityProfile{
				ProfileType:  runtime.SecurityProfile_Localhost,
				LocalhostRef: profileNamePrefix + "test-profile",
			},
		},
	} {
		t.Run(test.desc, func(t *testing.T) {
			asp := test.sp
			csp, err := GenerateApparmorSecurityProfile(test.profile)
			if err != nil {
				if test.expectErr {
					assert.Error(t, err)
				} else {
					assert.NoError(t, err)
				}
			} else {
				if asp == nil {
					asp = csp
				}
				specOpts, err := GenerateApparmorSpecOpts(asp, test.privileged, !test.disable)
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
						Linux:   &runtimespec.Linux{},
						Process: &runtimespec.Process{},
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
