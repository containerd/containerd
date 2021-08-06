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

	"github.com/containerd/containerd/containers"
	"github.com/containerd/containerd/namespaces"
)

func TestWithCPUCount(t *testing.T) {
	var (
		ctx = namespaces.WithNamespace(context.Background(), "testing")
		c   = containers.Container{ID: t.Name()}
		cpu = uint64(8)
		o   = WithWindowsCPUCount(cpu)
	)
	// Test with all three supported scenarios
	platforms := []string{"", "linux/amd64", "windows/amd64"}
	for _, p := range platforms {
		var spec *Spec
		var err error
		if p == "" {
			t.Log("Testing GenerateSpec default platform")
			spec, err = GenerateSpec(ctx, nil, &c, o)
		} else {
			t.Logf("Testing GenerateSpecWithPlatform with platform: '%s'", p)
			spec, err = GenerateSpecWithPlatform(ctx, nil, p, &c, o)
		}
		if err != nil {
			t.Fatalf("failed to generate spec with: %v", err)
		}
		if *spec.Windows.Resources.CPU.Count != cpu {
			t.Fatalf("spec.Windows.Resources.CPU.Count expected: %v, got: %v", cpu, *spec.Windows.Resources.CPU.Count)
		}
		if spec.Linux != nil && spec.Linux.Resources != nil && spec.Linux.Resources.CPU != nil {
			t.Fatalf("spec.Linux.Resources.CPU section should not be set on GOOS=windows")
		}
	}
}

func TestWithWindowsIgnoreFlushesDuringBoot(t *testing.T) {
	var (
		ctx = namespaces.WithNamespace(context.Background(), "testing")
		c   = containers.Container{ID: t.Name()}
		o   = WithWindowsIgnoreFlushesDuringBoot()
	)
	// Test with all supported scenarios
	platforms := []string{"", "windows/amd64"}
	for _, p := range platforms {
		var spec *Spec
		var err error
		if p == "" {
			t.Log("Testing GenerateSpec default platform")
			spec, err = GenerateSpec(ctx, nil, &c, o)
		} else {
			t.Logf("Testing GenerateSpecWithPlatform with platform: '%s'", p)
			spec, err = GenerateSpecWithPlatform(ctx, nil, p, &c, o)
		}
		if err != nil {
			t.Fatalf("failed to generate spec with: %v", err)
		}
		if spec.Windows.IgnoreFlushesDuringBoot != true {
			t.Fatalf("spec.Windows.IgnoreFlushesDuringBoot expected: true")
		}
	}
}

func TestWithWindowNetworksAllowUnqualifiedDNSQuery(t *testing.T) {
	var (
		ctx = namespaces.WithNamespace(context.Background(), "testing")
		c   = containers.Container{ID: t.Name()}
		o   = WithWindowNetworksAllowUnqualifiedDNSQuery()
	)
	// Test with all supported scenarios
	platforms := []string{"", "windows/amd64"}
	for _, p := range platforms {
		var spec *Spec
		var err error
		if p == "" {
			t.Log("Testing GenerateSpec default platform")
			spec, err = GenerateSpec(ctx, nil, &c, o)
		} else {
			t.Logf("Testing GenerateSpecWithPlatform with platform: '%s'", p)
			spec, err = GenerateSpecWithPlatform(ctx, nil, p, &c, o)
		}
		if err != nil {
			t.Fatalf("failed to generate spec with: %v", err)
		}
		if spec.Windows.Network.AllowUnqualifiedDNSQuery != true {
			t.Fatalf("spec.Windows.Network.AllowUnqualifiedDNSQuery expected: true")
		}
	}
}
