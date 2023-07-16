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

package log

import (
	"context"
	"reflect"
	"testing"
)

func TestLoggerContext(t *testing.T) {
	const expected = "one"
	ctx := context.Background()
	ctx = WithLogger(ctx, G(ctx).WithField("test", expected))
	if actual := GetLogger(ctx).Data["test"]; actual != expected {
		t.Errorf("expected: %v, got: %v", expected, actual)
	}
	a := G(ctx)
	b := GetLogger(ctx)
	if !reflect.DeepEqual(a, b) || a != b {
		t.Errorf("should be the same: %+v, %+v", a, b)
	}
}
