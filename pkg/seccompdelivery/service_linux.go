//go:build linux

package seccompdelivery

import (
	"context"

	"github.com/containerd/containerd/v2/pkg/securityprofile"
)

type service struct {
	delivery  *securityprofile.Delivery
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
	return s.delivery.Materialize(profileRef, labels, securityprofile.Errors{
		NotFound:    ErrProfileNotFound,
		InvalidData: ErrInvalidProfileData,
	})
}
