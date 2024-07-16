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

package lazyregexp

import (
	"testing"
)

func TestCompileOnce(t *testing.T) {
	t.Run("invalid regexp", func(t *testing.T) {
		defer func() {
			if r := recover(); r == nil {
				t.Errorf("expected a panic")
			}
		}()
		_ = New("[")
	})
	t.Run("valid regexp", func(t *testing.T) {
		re := New("[a-z]")
		ok := re.MatchString("hello")
		if !ok {
			t.Errorf("expected a match")
		}
	})
}
