package sbserver

import (
	"context"
	"errors"

	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

// RuntimeConfig returns configuration information of the runtime.
func (c *criService) RuntimeConfig(ctx context.Context, r *runtime.RuntimeConfigRequest) (*runtime.RuntimeConfigResponse, error) {
	return nil, errors.New("not implemented")
}
