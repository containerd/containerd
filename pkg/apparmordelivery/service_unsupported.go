//go:build !linux

package apparmordelivery

import "context"

type service struct{}

func NewService(*Config) (Service, error) {
	return nil, ErrUnsupported
}

func (s *service) EnsureProfile(context.Context, string, map[string]string) (string, error) {
	return "", ErrUnsupported
}
