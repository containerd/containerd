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

package instrument

import (
	"errors"
	"fmt"
	"testing"

	"github.com/containerd/log"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestLogLevelFor(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want log.Level
	}{
		{name: "grpc_not_found", err: status.Error(codes.NotFound, "no such container"), want: log.DebugLevel},
		{name: "wrapped_grpc_not_found", err: fmt.Errorf("container %q: %w", "id", status.Error(codes.NotFound, "no such container")), want: log.DebugLevel},
		{name: "grpc_unknown", err: status.Error(codes.Unknown, "boom"), want: log.ErrorLevel},
		{name: "plain_error", err: errors.New("boom"), want: log.ErrorLevel},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, logLevelFor(tc.err))
		})
	}
}
