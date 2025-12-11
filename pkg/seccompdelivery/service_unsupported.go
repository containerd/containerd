//go:build !linux

package seccompdelivery

import "context"

func NewService(_ *Config) (Service, error) {
	return unsupportedService{}, ErrUnsupported
}

type unsupportedService struct{}

func (unsupportedService) EnsureProfile(_ context.Context, _ string, _ map[string]string) (string, error) {
	return "", ErrUnsupported
}
