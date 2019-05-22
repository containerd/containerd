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

package errdefs

import (
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/pkg/errors"
)

func TestGRPCRoundTrip(t *testing.T) {
	errShouldLeaveAlone := errors.New("unknown to package")

	for _, testcase := range []struct {
		input error
		cause error
		str   string
	}{
		{
			input: ErrAlreadyExists,
			cause: ErrAlreadyExists,
		},
		{
			input: ErrNotFound,
			cause: ErrNotFound,
		},
		{
			input: errors.Wrapf(ErrFailedPrecondition, "test test test"),
			cause: ErrFailedPrecondition,
			str:   "test test test: failed precondition",
		},
		{
			input: status.Errorf(codes.Unavailable, "should be not available"),
			cause: ErrUnavailable,
			str:   "should be not available: unavailable",
		},
		{
			input: errShouldLeaveAlone,
			cause: ErrUnknown,
			str:   errShouldLeaveAlone.Error() + ": " + ErrUnknown.Error(),
		},
	} {
		t.Run(testcase.input.Error(), func(t *testing.T) {
			t.Logf("input: %v", testcase.input)
			gerr := ToGRPC(testcase.input)
			t.Logf("grpc: %v", gerr)
			ferr := FromGRPC(gerr)
			t.Logf("recovered: %v", ferr)

			if errors.Cause(ferr) != testcase.cause {
				t.Fatalf("unexpected cause: %v != %v", errors.Cause(ferr), testcase.cause)
			}

			expected := testcase.str
			if expected == "" {
				expected = testcase.cause.Error()
			}
			if ferr.Error() != expected {
				t.Fatalf("unexpected string: %q != %q", ferr.Error(), expected)
			}
		})
	}

}
