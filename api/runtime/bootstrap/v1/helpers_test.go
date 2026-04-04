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

package bootstrap

import (
	"strings"
	"testing"

	options "github.com/containerd/containerd/api/types/runc/options"
	"google.golang.org/protobuf/types/known/anypb"
)

func TestExtensions(t *testing.T) {
	params := &BootstrapParams{}

	err := params.AddExtension(&options.Options{ShimCgroup: "test-cgroup"})
	if err != nil {
		t.Fatalf("AddExtension: %v", err)
	}

	got := &options.Options{}
	found, err := params.FindExtension(got)
	if err != nil {
		t.Fatalf("FindExtension: %v", err)
	}
	if !found {
		t.Fatal("expected extension to be found")
	}
	if got.ShimCgroup != "test-cgroup" {
		t.Fatalf("expected ShimCgroup %q, got %q", "test-cgroup", got.ShimCgroup)
	}
}

func TestExtensionNotFound(t *testing.T) {
	params := &BootstrapParams{}

	got := &options.Options{}
	found, err := params.FindExtension(got)
	if err != nil {
		t.Fatalf("FindExtension: %v", err)
	}
	if found {
		t.Fatal("expected extension to not be found")
	}
}

func TestAddExtensionWithAny(t *testing.T) {
	params := &BootstrapParams{}

	ext := &options.Options{ShimCgroup: "test-cgroup"}
	anyVal, err := anypb.New(ext)
	if err != nil {
		t.Fatalf("anypb.New: %v", err)
	}

	err = params.AddExtension(anyVal)
	if err != nil {
		t.Fatalf("AddExtension: %v", err)
	}

	got := &options.Options{}
	found, err := params.FindExtension(got)
	if err != nil {
		t.Fatalf("FindExtension: %v", err)
	}
	if !found {
		t.Fatal("expected extension to be found")
	}
	if got.ShimCgroup != "test-cgroup" {
		t.Fatalf("expected ShimCgroup %q, got %q", "test-cgroup", got.ShimCgroup)
	}

	if !strings.Contains(params.Extensions[0].Value.TypeUrl, "Options") {
		t.Fatalf("expected TypeUrl to contain %q, got %q", "Options", params.Extensions[0].Value.TypeUrl)
	}
}
