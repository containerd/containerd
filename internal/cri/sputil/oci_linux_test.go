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
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/containerd/containerd/v2/contrib/seccomp"
	"github.com/containerd/errdefs"
	digest "github.com/opencontainers/go-digest"
	specs "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
	merger "sigs.k8s.io/security-profiles-merger/seccomp"
)

func TestOCISeccompMerge(t *testing.T) {
	for _, local := range []bool{false, true} {
		t.Run(map[bool]string{false: "default floor", true: "local base preserves default floor"}[local], func(t *testing.T) {
			artifact := &specs.LinuxSeccomp{DefaultAction: specs.ActAllow, Syscalls: []specs.LinuxSyscall{{Names: []string{"getppid"}, Action: specs.ActErrno}}}
			profile := &runtime.SecurityProfile{ProfileType: runtime.SecurityProfile_OCI, OciRef: "registry.example/profile@" + digest.FromString("manifest").String()}
			s := &specs.Spec{Linux: &specs.Linux{}, Process: &specs.Process{Capabilities: &specs.LinuxCapabilities{}}}
			baseline := seccomp.DefaultProfile(s)
			inputs := []*specs.LinuxSeccomp{baseline}
			if local {
				localProfile := &specs.LinuxSeccomp{DefaultAction: specs.ActAllow, Syscalls: []specs.LinuxSyscall{{Names: []string{"getpid"}, Action: specs.ActErrno}}}
				data, err := json.Marshal(localProfile)
				require.NoError(t, err)
				path := filepath.Join(t.TempDir(), "base.json")
				require.NoError(t, os.WriteFile(path, data, 0600))
				profile.BaseProfile = &runtime.SecurityProfileBase{Type: runtime.SecurityProfileBase_Localhost, LocalhostRef: path}
				inputs = append(inputs, localProfile)
			}
			inputs = append(inputs, artifact)
			expected, err := merger.Intersect(inputs...)
			require.NoError(t, err)
			option, err := GenerateSeccompSpecOpts(profile, false, true, func(string) (*specs.LinuxSeccomp, error) { return artifact, nil })
			require.NoError(t, err)
			require.NoError(t, option(context.Background(), nil, nil, s))
			require.Equal(t, expected, s.Linux.Seccomp)
			// An allow-all overlay still cannot grant what the runtime baseline denies.
			diff, err := merger.Diff(artifact, s.Linux.Seccomp)
			require.NoError(t, err)
			require.False(t, diff.Equal)
			require.NotSame(t, artifact, s.Linux.Seccomp)
			action := func(name string) specs.LinuxSeccompAction {
				for _, rule := range s.Linux.Seccomp.Syscalls {
					if slices.Contains(rule.Names, name) {
						require.Empty(t, rule.Args)
						return rule.Action
					}
				}
				return s.Linux.Seccomp.DefaultAction
			}
			require.Equal(t, specs.ActAllow, action("read"))
			require.Equal(t, specs.ActErrno, action("getppid")) // overlay denial
			require.Equal(t, specs.ActErrno, action("mount"))   // runtime baseline denial
			if local {
				require.Equal(t, specs.ActErrno, action("getpid"))
			} else {
				require.Equal(t, specs.ActAllow, action("getpid"))
			}
		})
	}
}

func TestOCISeccompInvalidProfileErrors(t *testing.T) {
	ref := "registry.example/profile@" + digest.FromString("missing-profile").String()
	profile := &runtime.SecurityProfile{ProfileType: runtime.SecurityProfile_OCI, OciRef: ref}
	spec := &specs.Spec{
		Linux:   &specs.Linux{},
		Process: &specs.Process{Capabilities: &specs.LinuxCapabilities{}},
	}
	option, err := GenerateSeccompSpecOpts(profile, false, true, func(string) (*specs.LinuxSeccomp, error) {
		return nil, errdefs.ErrNotFound
	})
	require.NoError(t, err)
	err = option(context.Background(), nil, nil, spec)
	require.Equal(t, codes.InvalidArgument, status.Code(err))

	profile.BaseProfile = &runtime.SecurityProfileBase{
		Type:         runtime.SecurityProfileBase_Localhost,
		LocalhostRef: filepath.Join(t.TempDir(), "missing.json"),
	}
	option, err = GenerateSeccompSpecOpts(profile, false, true, func(string) (*specs.LinuxSeccomp, error) {
		return &specs.LinuxSeccomp{DefaultAction: specs.ActAllow}, nil
	})
	require.NoError(t, err)
	err = option(context.Background(), nil, nil, spec)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}
