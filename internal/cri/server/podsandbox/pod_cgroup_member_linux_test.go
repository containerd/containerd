//go:build linux

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

package podsandbox

import (
	"context"
	"testing"

	"github.com/containerd/containerd/v2/pkg/oci"
	"github.com/containerd/errdefs"
	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/stretchr/testify/require"
)

type fakePodCgroupMember struct {
	id   string
	spec *oci.Spec
	err  error
}

func (m fakePodCgroupMember) ID() string { return m.id }

func (m fakePodCgroupMember) Spec(context.Context) (*oci.Spec, error) { return m.spec, m.err }

func TestCheckPodCgroupMember(t *testing.T) {
	withCgroupsPath := func(path string) *oci.Spec {
		return &oci.Spec{Linux: &runtimespec.Linux{CgroupsPath: path}}
	}
	for _, test := range []struct {
		name    string
		member  fakePodCgroupMember
		wantErr error
	}{
		{name: "in pod cgroup", member: fakePodCgroupMember{id: "c1", spec: withCgroupsPath("/kubepods/pod-1/c1")}},
		{name: "other pod", member: fakePodCgroupMember{id: "c1", spec: withCgroupsPath("/kubepods/pod-2/c1")}, wantErr: errdefs.ErrInvalidArgument},
		{name: "ancestor cgroup", member: fakePodCgroupMember{id: "c1", spec: withCgroupsPath("/kubepods/c1")}, wantErr: errdefs.ErrInvalidArgument},
		{name: "no linux spec", member: fakePodCgroupMember{id: "c1", spec: &oci.Spec{}}, wantErr: errdefs.ErrInvalidArgument},
		{name: "spec lookup fails", member: fakePodCgroupMember{id: "c1", err: errdefs.ErrNotFound}, wantErr: errdefs.ErrNotFound},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := checkPodCgroupMember(t.Context(), test.member, "/kubepods/pod-1")
			if test.wantErr == nil {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, test.wantErr)
		})
	}
}
