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

package store

import (
	"testing"

	"github.com/containerd/containerd/errdefs"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestStoreErrAlreadyExistGRPCStatus(t *testing.T) {
	err := errdefs.ToGRPC(errdefs.ErrAlreadyExists)
	s, ok := status.FromError(err)
	if !ok {
		t.Fatalf("failed to convert err: %v to status: %d", err, codes.AlreadyExists)
	}
	if s.Code() != codes.AlreadyExists {
		t.Fatalf("expected code: %d got: %d", codes.AlreadyExists, s.Code())
	}
}

func TestStoreErrNotExistGRPCStatus(t *testing.T) {
	err := errdefs.ToGRPC(errdefs.ErrNotFound)
	s, ok := status.FromError(err)
	if !ok {
		t.Fatalf("failed to convert err: %v to status: %d", err, codes.NotFound)
	}
	if s.Code() != codes.NotFound {
		t.Fatalf("expected code: %d got: %d", codes.NotFound, s.Code())
	}
}
