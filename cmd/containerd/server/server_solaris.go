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

package server

import (
	"context"

	srvconfig "github.com/containerd/containerd/v2/cmd/containerd/server/config"
	"google.golang.org/grpc"
)

func apply(_ context.Context, _ *srvconfig.Config) error {
	return nil
}

// setupTLSFromWindowsCertStore is a NOOP on non-Windows platforms.
func setupTLSFromWindowsCertStore(ctx context.Context, config *srvconfig.Config) ([]grpc.ServerOption, error) {
	return nil, nil
}
