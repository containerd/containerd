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

package sandbox

import (
	"reflect"
	"testing"
	"time"

	"github.com/containerd/typeurl"
	runtime "github.com/opencontainers/runtime-spec/specs-go"
	"google.golang.org/protobuf/types/known/anypb"
	"gotest.tools/v3/assert"
	is "gotest.tools/v3/assert/cmp"
)

func init() {
	typeurl.Register(&runtime.Spec{}, "")
}

func TestSpecToAny(t *testing.T) {
	inSpec := &runtime.Spec{
		Version: "1",
		Linux:   &runtime.Linux{CgroupsPath: "/test"},
	}

	any, err := toAny(inSpec)
	assert.NilError(t, err)

	outSpec, err := anyToSpec(any)
	assert.NilError(t, err)

	assert.Check(t, is.Equal(inSpec.Version, outSpec.Version))
	assert.Check(t, is.Equal(inSpec.Linux.CgroupsPath, outSpec.Linux.CgroupsPath))
}

func TestSandboxConv(t *testing.T) {
	in := Sandbox{
		ID: "123",
		Spec: &runtime.Spec{
			Version: "1",
			Linux:   &runtime.Linux{CgroupsPath: "/test"},
		},
		Containers: []Container{
			{
				ID:         "container1",
				Labels:     nil,
				Extensions: nil,
			},
		},
		Labels:     map[string]string{"a": "b"},
		CreatedAt:  time.Now().UTC(),
		UpdatedAt:  time.Now().UTC().Add(1 * time.Hour),
		Extensions: map[string]typeurl.Any{"a": &anypb.Any{TypeUrl: "test", Value: []byte{1}}},
	}

	proto, err := ToProto(&in)
	if err != nil {
		t.Fatal(err)
	}

	out, err := FromProto(proto)
	if err != nil {
		t.Fatal(err)
	}

	if in.ID != out.ID {
		t.Fatalf("unexpected id: %q != %q", in.ID, out.ID)
	}

	if !reflect.DeepEqual(in.Labels, out.Labels) {
		t.Fatalf("unexpected labels: %+v != %+v", in.Labels, out.Labels)
	}

	if !reflect.DeepEqual(in.Spec, out.Spec) {
		t.Fatalf("unexpected spec: %+v != %+v", in.Spec, out.Spec)
	}

	if !reflect.DeepEqual(in.Extensions, out.Extensions) {
		t.Fatalf("unexpected extensions: %+v != %+v", in.Extensions, out.Extensions)
	}

	if in.CreatedAt.UnixNano() != out.CreatedAt.UnixNano() {
		t.Fatalf("unexpected created time: %+v != %+v", in.CreatedAt, out.CreatedAt)
	}

	if in.UpdatedAt.UnixNano() != out.UpdatedAt.UnixNano() {
		t.Fatalf("unexpected updated time: %+v != %+v", in.UpdatedAt, out.UpdatedAt)
	}
}

func TestStatusConv(t *testing.T) {
	in := Status{
		ID:      "2345",
		State:   StateStarting,
		Version: "1.0.1",
		Extra: map[string]typeurl.Any{
			"a": &anypb.Any{TypeUrl: "test", Value: []byte{1}},
			"b": &anypb.Any{},
		},
	}

	proto := StatusToProto(in)
	out := StatusFromProto(proto)

	if !reflect.DeepEqual(in, out) {
		t.Fatalf("unexpected conv: %+v != %+v", in, out)
	}
}
