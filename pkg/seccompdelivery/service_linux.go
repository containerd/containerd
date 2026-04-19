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

package seccompdelivery

import (
	"context"

	"github.com/containerd/containerd/v2/pkg/securityprofile"
)

type service struct {
	delivery *securityprofile.Delivery
}

func NewService(cfg *Config) (Service, error) {
	n := securityprofile.Normalize(cfg, defaultConfig)
	d, err := securityprofile.New(n.LabelPrefix, n.TargetDir)
	if err != nil {
		return nil, err
	}
	return &service{delivery: d}, nil
}

func (s *service) EnsureProfile(ctx context.Context, profileRef string, labels map[string]string) (string, error) {
	path, _, err := s.delivery.Materialize(profileRef, labels, securityprofile.Errors{
		NotFound:    ErrProfileNotFound,
		InvalidData: ErrInvalidProfileData,
	})
	return path, err
}
