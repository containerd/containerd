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
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
)

func TestInvalidArgument(t *testing.T) {
	for _, match := range []error{
		ErrInvalidArgument,
		&errInvalidArgument{},
		&customInvalidArgument{},
		&wrappedInvalidArgument{errors.New("invalid parameter")},
	} {
		if !IsInvalidArgument(match) {
			t.Errorf("error did not match invalid argument: %#v", match)
		}
	}
	for _, nonMatch := range []error{
		ErrUnknown,
		context.Canceled,
		errors.New("invalid argument"),
	} {
		if IsInvalidArgument(nonMatch) {
			t.Errorf("error unexpectedly matched invalid argument: %#v", nonMatch)
		}
	}
}

func TestErrorEquivalence(t *testing.T) {
	var e1 error = ErrAborted
	var e2 error = ErrUnknown
	if e1 == e2 {
		t.Fatal("should not equal the same error")
	}
	if errors.Is(e1, e2) {
		t.Fatal("errors.Is should not return true")
	}

	var e3 error = errAborted{}
	if e1 != e3 {
		t.Fatal("new instance should be equivalent")
	}
	if !errors.Is(e1, e3) {
		t.Fatal("errors.Is should be true")
	}
	if !errors.Is(e3, e1) {
		t.Fatal("errors.Is should be true")
	}
	var aborted errAborted
	if !errors.As(e1, &aborted) {
		t.Fatal("errors.As should be true")
	}

	var e4 = ErrAborted.WithMessage("custom message")
	if e1 == e4 {
		t.Fatal("should not equal the same error")
	}

	if !errors.Is(e4, e1) {
		t.Fatal("errors.Is should be true, e1 is in the tree of e4")
	}

	if errors.Is(e1, e4) {
		t.Fatal("errors.Is should be false, e1 is not a custom message")
	}

	if !errors.As(e4, &aborted) {
		t.Fatal("errors.As should be true")
	}

	var custom customMessage
	if !errors.As(e4, &custom) {
		t.Fatal("errors.As should be true")
	}
	if custom.msg != "custom message" {
		t.Fatalf("unexpected custom message: %q", custom.msg)
	}
	if custom.err != e1 {
		t.Fatalf("unexpected custom message error: %v", custom.err)
	}
}

func TestWithMessage(t *testing.T) {
	testErrors := []error{ErrUnknown,
		ErrInvalidArgument,
		ErrNotFound,
		ErrAlreadyExists,
		ErrPermissionDenied,
		ErrResourceExhausted,
		ErrFailedPrecondition,
		ErrConflict,
		ErrNotModified,
		ErrAborted,
		ErrOutOfRange,
		ErrNotImplemented,
		ErrInternal,
		ErrUnavailable,
		ErrDataLoss,
		ErrUnauthenticated,
	}
	for _, err := range testErrors {
		e1 := err
		t.Run(err.Error(), func(t *testing.T) {
			wm, ok := e1.(interface{ WithMessage(string) error })
			if !ok {
				t.Fatal("WithMessage not supported")
			}
			e2 := wm.WithMessage("custom message")

			if e1 == e2 {
				t.Fatal("should not equal the same error")
			}

			if !errors.Is(e2, e1) {
				t.Fatal("errors.Is should return true")
			}

			if errors.Is(e1, e2) {
				t.Fatal("errors.Is should be false, e1 is not a custom message")
			}

			var raw = reflect.New(reflect.TypeOf(e1)).Interface()
			if !errors.As(e2, raw) {
				t.Fatal("errors.As should be true")
			}

			var custom customMessage
			if !errors.As(e2, &custom) {
				t.Fatal("errors.As should be true")
			}
			if custom.msg != "custom message" {
				t.Fatalf("unexpected custom message: %q", custom.msg)
			}
			if custom.err != e1 {
				t.Fatalf("unexpected custom message error: %v", custom.err)
			}

		})
	}
}

func TestInterfaceMatch(t *testing.T) {
	testCases := []struct {
		err   error
		check func(error) bool
	}{
		{ErrUnknown, isInterface[unknown]},
		{ErrInvalidArgument, isInterface[invalidParameter]},
		{ErrNotFound, isInterface[notFound]},
		{ErrAlreadyExists, isInterface[alreadyExists]},
		{ErrPermissionDenied, isInterface[forbidden]},
		{ErrResourceExhausted, isInterface[resourceExhausted]},
		{ErrFailedPrecondition, isInterface[failedPrecondition]},
		{ErrConflict, isInterface[conflict]},
		{ErrNotModified, isInterface[notModified]},
		{ErrAborted, isInterface[aborted]},
		{ErrOutOfRange, isInterface[outOfRange]},
		{ErrNotImplemented, isInterface[notImplemented]},
		{ErrInternal, isInterface[system]},
		{ErrUnavailable, isInterface[unavailable]},
		{ErrDataLoss, isInterface[dataLoss]},
		{ErrUnauthenticated, isInterface[unauthorized]},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(fmt.Sprintf("%T", tc.err), func(t *testing.T) {
			if !tc.check(tc.err) {
				t.Fatal("Error does not match interface")
			}
		})
	}
}

type customInvalidArgument struct{}

func (*customInvalidArgument) Error() string {
	return "my own invalid argument"
}

func (*customInvalidArgument) InvalidParameter() {}

type wrappedInvalidArgument struct{ error }

func (*wrappedInvalidArgument) InvalidParameter() {}
