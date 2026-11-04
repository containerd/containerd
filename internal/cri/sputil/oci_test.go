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
	"testing"

	digest "github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

func TestValidateOCIFields(t *testing.T) {
	ref := "registry.example/profile@" + digest.FromString("manifest").String()
	for _, tc := range []struct {
		name                       string
		profile                    *runtime.SecurityProfile
		seccomp, privileged, valid bool
	}{
		{"unset", nil, true, false, true},
		{"OCI", &runtime.SecurityProfile{ProfileType: runtime.SecurityProfile_OCI, OciRef: ref}, true, false, true},
		{"default base", &runtime.SecurityProfile{ProfileType: runtime.SecurityProfile_OCI, OciRef: ref, BaseProfile: &runtime.SecurityProfileBase{}}, true, false, true},
		{"local base", &runtime.SecurityProfile{ProfileType: runtime.SecurityProfile_OCI, OciRef: ref, BaseProfile: &runtime.SecurityProfileBase{Type: runtime.SecurityProfileBase_Localhost, LocalhostRef: "/profiles/base.json"}}, true, false, true},
		{"OCI field on default", &runtime.SecurityProfile{OciRef: ref}, true, false, false},
		{"base on default", &runtime.SecurityProfile{BaseProfile: &runtime.SecurityProfileBase{}}, true, false, false},
		{"missing ref", &runtime.SecurityProfile{ProfileType: runtime.SecurityProfile_OCI}, true, false, false},
		{"tag", &runtime.SecurityProfile{ProfileType: runtime.SecurityProfile_OCI, OciRef: "registry.example/profile:latest"}, true, false, false},
		{"localhost field", &runtime.SecurityProfile{ProfileType: runtime.SecurityProfile_OCI, OciRef: ref, LocalhostRef: "/profile"}, true, false, false},
		{"relative base", &runtime.SecurityProfile{ProfileType: runtime.SecurityProfile_OCI, OciRef: ref, BaseProfile: &runtime.SecurityProfileBase{Type: runtime.SecurityProfileBase_Localhost, LocalhostRef: "relative"}}, true, false, false},
		{"parent traversal base", &runtime.SecurityProfile{ProfileType: runtime.SecurityProfile_OCI, OciRef: ref, BaseProfile: &runtime.SecurityProfileBase{Type: runtime.SecurityProfileBase_Localhost, LocalhostRef: "/profiles/../etc/seccomp.json"}}, true, false, false},
		{"parent traversal base suffix", &runtime.SecurityProfile{ProfileType: runtime.SecurityProfile_OCI, OciRef: ref, BaseProfile: &runtime.SecurityProfileBase{Type: runtime.SecurityProfileBase_Localhost, LocalhostRef: "/profiles/.."}}, true, false, false},
		{"default base path", &runtime.SecurityProfile{ProfileType: runtime.SecurityProfile_OCI, OciRef: ref, BaseProfile: &runtime.SecurityProfileBase{LocalhostRef: "/profile"}}, true, false, false},
		{"unknown base", &runtime.SecurityProfile{ProfileType: runtime.SecurityProfile_OCI, OciRef: ref, BaseProfile: &runtime.SecurityProfileBase{Type: 99}}, true, false, false},
		{"apparmor", &runtime.SecurityProfile{ProfileType: runtime.SecurityProfile_OCI, OciRef: ref}, false, false, false},
		{"privileged", &runtime.SecurityProfile{ProfileType: runtime.SecurityProfile_OCI, OciRef: ref}, true, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateOCIFields(tc.profile, tc.seccomp, tc.privileged)
			if tc.valid {
				require.NoError(t, err)
			} else {
				require.Equal(t, codes.InvalidArgument, status.Code(err))
			}
		})
	}
}
