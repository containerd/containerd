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

package logtest

import (
	"context"
	"testing"

	"github.com/containerd/log/logtest"
)

// WithT adds a logging hook for the given test
// Changes debug level to debug, clears output, and
// outputs all log messages as test logs.
//
// Deprecated: use [logtest.WithT].
func WithT(ctx context.Context, t testing.TB) context.Context {
	return logtest.WithT(ctx, t)
}
