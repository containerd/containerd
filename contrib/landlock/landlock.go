// +build linux

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

package landlock

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/containerd/containerd/containers"
	"github.com/containerd/containerd/oci"
	specs "github.com/opencontainers/runtime-spec/specs-go"
)

// loadSpec loads the specification from the provided path.
func loadLandlock(cPath string) (landlockSpec *specs.Landlock, err error) {
	cf, err := os.Open(cPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("landlock JSON specification file %s not found", cPath)
		}
		return nil, err
	}
	defer cf.Close()

	if err = json.NewDecoder(cf).Decode(&landlockSpec); err != nil {
		return nil, err
	}
	return landlockSpec, err
}

// WithProfile sets the provided landlock profile to the spec
func WithProfile(profile string) oci.SpecOpts {
	return func(_ context.Context, _ oci.Client, _ *containers.Container, s *specs.Spec) error {		
		landlock, err := loadLandlock(profile)
		if err != nil {
			return err
		}
		s.Process.Landlock = landlock
		return nil
	}
}

// Supporting a default landlock profile is under discussion with community: https://github.com/containerd/containerd/issues/6056
func WithDefaultProfile() oci.SpecOpts {
	return func(_ context.Context, _ oci.Client, _ *containers.Container, s *specs.Spec) error {		
		return nil
	}
}