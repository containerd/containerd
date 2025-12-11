package seccompdelivery

import (
	"context"
	"errors"

	"github.com/containerd/containerd/v2/pkg/securityprofile"
)

const (
	DefaultLabelPrefix = "io.containerd.seccomp.localhost/"
	DefaultTargetDir = "/var/lib/kubelet/seccomp"
)

var (
	ErrProfileNotFound = errors.New("profile not found in image labels")
	ErrInvalidProfileData = errors.New("invalid profile data")
	ErrUnsupported = errors.New("seccomp profile delivery unsupported on this platform")
)

type Service interface {
	EnsureProfile(ctx context.Context, profileRef string, labels map[string]string) (string, error)
}

type Config = securityprofile.Config

var defaultConfig = securityprofile.Config{
	LabelPrefix: DefaultLabelPrefix,
	TargetDir:   DefaultTargetDir,
}

func normalizeConfig(cfg *Config) securityprofile.Config {
	return securityprofile.Normalize(cfg, defaultConfig)
}
