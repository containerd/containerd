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

package apparmordelivery

import (
	"context"
	"fmt"
	"os/exec"

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
	if err != nil {
		return "", err
	}
	if err := loadProfile(ctx, path); err != nil {
		return "", fmt.Errorf("load apparmor profile %q: %w", profileRef, err)
	}
	return path, nil
}

func loadProfile(ctx context.Context, path string) error {
	parser, err := exec.LookPath("apparmor_parser")
	if err != nil {
		parser = "/sbin/apparmor_parser"
	}
	output, err := exec.CommandContext(ctx, parser, "-Kr", path).CombinedOutput()
	if err != nil {
		return fmt.Errorf("apparmor_parser error: %v; output: %s", err, output)
	}
	return nil
}
