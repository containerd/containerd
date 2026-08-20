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

package shim

import (
	"context"
	"errors"
	"runtime"
	"testing"

	bootapi "github.com/containerd/containerd/api/runtime/bootstrap/v1"
	"github.com/containerd/containerd/v2/pkg/protobuf/proto"
	"github.com/containerd/containerd/v2/pkg/protobuf/types"
	googleproto "google.golang.org/protobuf/proto"
)

func TestRuntimeWithEmptyMaxEnvProcs(t *testing.T) {
	var oldGoMaxProcs = runtime.GOMAXPROCS(0)
	defer runtime.GOMAXPROCS(oldGoMaxProcs)

	t.Setenv("GOMAXPROCS", "")
	setRuntime()

	var currentGoMaxProcs = runtime.GOMAXPROCS(0)
	if currentGoMaxProcs != 2 {
		t.Fatal("the max number of procs should be 2")
	}
}

func TestRuntimeWithNonEmptyMaxEnvProcs(t *testing.T) {
	t.Setenv("GOMAXPROCS", "not_empty")
	setRuntime()
	var oldGoMaxProcs2 = runtime.GOMAXPROCS(0)
	if oldGoMaxProcs2 != runtime.NumCPU() {
		t.Fatal("the max number CPU should be equal to available CPUs")
	}
}

func TestShimOptWithValue(t *testing.T) {
	ctx := context.TODO()
	ctx = context.WithValue(ctx, OptsKey{}, Opts{Debug: true})

	o := ctx.Value(OptsKey{})
	if o == nil {
		t.Fatal("opts nil")
	}
	op, ok := o.(Opts)
	if !ok {
		t.Fatal("opts not of type Opts")
	}
	if !op.Debug {
		t.Fatal("opts.Debug should be true")
	}
}

func TestParseBootstrapParams(t *testing.T) {
	const (
		id        = "container-id"
		namespace = "moby"
	)

	t.Run("bootstrap protocol", func(t *testing.T) {
		input, err := proto.Marshal(&bootapi.BootstrapParams{
			InstanceID: id,
			Namespace:  namespace,
		})
		if err != nil {
			t.Fatal(err)
		}

		params, err := parseBootstrapParams(input, id, namespace)
		if err != nil {
			t.Fatal(err)
		}
		if params.InstanceID != id || params.Namespace != namespace {
			t.Fatalf("bootstrap params not preserved: %+v", params)
		}
	})

	t.Run("mismatched identity", func(t *testing.T) {
		input, err := proto.Marshal(&bootapi.BootstrapParams{
			InstanceID: id,
			Namespace:  namespace,
		})
		if err != nil {
			t.Fatal(err)
		}

		_, err = parseBootstrapParams(input, "different-id", namespace)
		if err == errDeprecatedBootstrapAPI {
			t.Fatal("expected details for mismatched bootstrap parameters")
		}
		if !errors.Is(err, errDeprecatedBootstrapAPI) {
			t.Fatalf("expected deprecated bootstrap API error, got %v", err)
		}
	})

	t.Run("deprecated protocol", func(t *testing.T) {
		input, err := proto.Marshal(&types.Any{
			TypeUrl: "types.containerd.io/containerd.runc.v1.Options",
			Value:   []byte("runc options"),
		})
		if err != nil {
			t.Fatal(err)
		}

		_, err = parseBootstrapParams(input, id, namespace)
		if !errors.Is(err, errDeprecatedBootstrapAPI) {
			t.Fatalf("expected deprecated bootstrap API error, got %v", err)
		}
	})

	t.Run("malformed bootstrap protocol", func(t *testing.T) {
		input := []byte{0xff}
		_, err := parseBootstrapParams(input, id, namespace)
		if !errors.Is(err, errDeprecatedBootstrapAPI) {
			t.Fatalf("expected deprecated bootstrap API error, got %v", err)
		}
		if !errors.Is(err, googleproto.Error) {
			t.Fatalf("expected protobuf error, got %v", err)
		}
	})
}
