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

package v2

import (
	"errors"
	"os"
	"testing"
)

func TestCheckCopyShimLogError(t *testing.T) {
	ctx := t.Context()
	testError := errors.New("test error")

	if err := checkCopyShimLogError(ctx, nil); err != nil {
		t.Fatalf("should return the actual error except ErrNotExist, but %v", err)
	}
	if err := checkCopyShimLogError(ctx, testError); err != testError {
		t.Fatalf("should return the actual error except ErrNotExist, but %v", err)
	}
	if err := checkCopyShimLogError(ctx, os.ErrNotExist); err != nil {
		t.Fatalf("should return nil for ErrNotExist, but %v", err)
	}
}
