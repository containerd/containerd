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

package opts

import (
	"context"

	"github.com/containerd/cgroups/v3"
	cgroup1 "github.com/containerd/cgroups/v3/cgroup1"
	cgroup2 "github.com/containerd/cgroups/v3/cgroup2"
	"github.com/containerd/containerd/namespaces"
)

// WithNamespaceCgroupDeletion removes the cgroup directory that was created for the namespace
func WithNamespaceCgroupDeletion(ctx context.Context, i *namespaces.DeleteInfo) error {
	if cgroups.Mode() == cgroups.Unified {
		cg, err := cgroup2.Load(i.Name)
		if err != nil {
			return err
		}
		return cg.Delete()
	}
	cg, err := cgroup1.Load(cgroup1.StaticPath(i.Name))
	if err != nil {
		if err == cgroup1.ErrCgroupDeleted {
			return nil
		}
		return err
	}
	return cg.Delete()
}
