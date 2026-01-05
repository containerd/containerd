//go:build !windows

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

package oci

import (
	"context"
	"testing"

	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/opencontainers/runtime-spec/specs-go"
)

func TestWithDefaultPathEnv(t *testing.T) {
	t.Parallel()
	s := Spec{}
	s.Process = &specs.Process{
		Env: []string{},
	}
	var (
		defaultUnixEnv = "PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
		ctx            = namespaces.WithNamespace(context.Background(), "test")
	)
	WithDefaultPathEnv(ctx, nil, nil, &s)
	if !Contains(s.Process.Env, defaultUnixEnv) {
		t.Fatal("default Unix Env not found")
	}
}
