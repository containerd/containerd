//go:build linux

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
	n := normalizeConfig(cfg)
	d, err := securityprofile.New(n.LabelPrefix, n.TargetDir)
	if err != nil {
		return nil, err
	}
	return &service{delivery: d}, nil
}

func (s *service) EnsureProfile(ctx context.Context, profileRef string, labels map[string]string) (string, error) {
	path, err := s.delivery.Materialize(profileRef, labels, securityprofile.Errors{
		NotFound:    ErrProfileNotFound,
		InvalidData: ErrInvalidProfileData,
	})
	if err != nil {
		return "", err
	}
	if err := loadProfile(path); err != nil {
		return "", fmt.Errorf("load apparmor profile %q: %w", profileRef, err)
	}
	return path, nil
}

func loadProfile(path string) error {
	output, err := exec.Command("apparmor_parser", "-Kr", path).CombinedOutput()
	if err != nil {
		if string(output) != "" {
			return fmt.Errorf("apparmor_parser: %s", output)
		}
		return fmt.Errorf("apparmor_parser: %w", err)
	}
	return nil
}
