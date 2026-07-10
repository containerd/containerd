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

	t.Run("new bootstrap protocol", func(t *testing.T) {
		want := &bootapi.BootstrapParams{
			InstanceID: id,
			Namespace:  namespace,
		}
		input, err := proto.Marshal(want)
		if err != nil {
			t.Fatal(err)
		}

		got, usesBootstrapProtocol := parseBootstrapParams(input, id, namespace)
		if !usesBootstrapProtocol {
			t.Fatal("new bootstrap params detected as deprecated")
		}
		if !googleproto.Equal(got, want) {
			t.Fatalf("bootstrap params not preserved: got %+v, want %+v", got, want)
		}
	})

	t.Run("2.2 runtime options Any", func(t *testing.T) {
		// Containerd 2.2 sends an Any whose fields decode as non-empty
		// BootstrapParams ID and namespace values.
		input, err := proto.Marshal(&types.Any{
			TypeUrl: "types.containerd.io/containerd.runc.v1.Options",
			Value:   []byte("runc options"),
		})
		if err != nil {
			t.Fatal(err)
		}

		var collided bootapi.BootstrapParams
		if err := proto.Unmarshal(input, &collided); err != nil {
			t.Fatal(err)
		}
		if collided.InstanceID == "" || collided.Namespace == "" {
			t.Fatalf("test payload did not reproduce field collision: %+v", &collided)
		}

		params, usesBootstrapProtocol := parseBootstrapParams(input, id, namespace)
		if usesBootstrapProtocol {
			t.Fatal("containerd 2.2 runtime options detected as new bootstrap params")
		}
		if !googleproto.Equal(params, &bootapi.BootstrapParams{}) {
			t.Fatalf("legacy payload fields preserved: %+v", params)
		}
	})
}

func TestMarshalBootstrapResponse(t *testing.T) {
	result := &bootapi.BootstrapResult{
		Version:  3,
		Address:  "unix:///run/containerd/shim.sock",
		Protocol: "ttrpc",
		Metadata: map[string]string{
			"unsupported": "value",
		},
	}

	t.Run("legacy JSON", func(t *testing.T) {
		data, err := marshalBootstrapResponse(result, false)
		if err != nil {
			t.Fatal(err)
		}
		want := `{"version":3,"address":"unix:///run/containerd/shim.sock","protocol":"ttrpc"}`
		if got := string(data); got != want {
			t.Fatalf("unexpected legacy response: got %q, want %q", got, want)
		}
	})

	t.Run("bootstrap protobuf", func(t *testing.T) {
		data, err := marshalBootstrapResponse(result, true)
		if err != nil {
			t.Fatal(err)
		}

		var got bootapi.BootstrapResult
		if err := proto.Unmarshal(data, &got); err != nil {
			t.Fatal(err)
		}
		if !googleproto.Equal(&got, result) {
			t.Fatalf("bootstrap result not preserved: got %+v, want %+v", &got, result)
		}
	})
}
