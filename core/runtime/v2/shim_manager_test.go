//go:build !windows && !darwin

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
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/containerd/typeurl/v2"

	apitypes "github.com/containerd/containerd/api/types"
	runcoptions "github.com/containerd/containerd/api/types/runc/options"

	"github.com/containerd/containerd/v2/pkg/protobuf/proto"
	"github.com/containerd/containerd/v2/pkg/protobuf/types"
)

func setupFakeShimBinary(t *testing.T, info *apitypes.RuntimeInfo) (shimPath string, receivedOptionsPath string) {
	t.Helper()

	dir := t.TempDir()
	infoPath := filepath.Join(dir, "runtime-info.pb")
	receivedOptionsPath = filepath.Join(dir, "received-options.pb")
	shimPath = filepath.Join(dir, "containerd-shim-fake-v1")

	infoB, err := proto.Marshal(info)
	if err != nil {
		t.Fatalf("failed to marshal RuntimeInfo: %v", err)
	}
	if err := os.WriteFile(infoPath, infoB, 0o644); err != nil {
		t.Fatal(err)
	}

	script := fmt.Sprintf("#!/bin/sh\ncat > %q\nexec cat %q\n", receivedOptionsPath, infoPath)
	if err := os.WriteFile(shimPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return shimPath, receivedOptionsPath
}

func TestLoadShimInfoPassesRuntimeOptions(t *testing.T) {
	shimPath, receivedOptionsPath := setupFakeShimBinary(t, &apitypes.RuntimeInfo{
		Name: "fake",
		Annotations: map[string]string{
			allowedMounts: "format/*,overlay",
		},
	})

	runtimeOptions, err := typeurl.MarshalAny(&runcoptions.Options{
		BinaryName: "/usr/local/bin/crun",
	})
	if err != nil {
		t.Fatal(err)
	}

	sm := &ShimManager{}
	sinfo, err := sm.loadShimInfo(context.Background(), shimPath, runtimeOptions)
	if err != nil {
		t.Fatalf("loadShimInfo failed: %v", err)
	}

	if want := []string{"format/*", "overlay"}; !slices.Equal(sinfo.handledMounts, want) {
		t.Errorf("expected handledMounts %v, got %v", want, sinfo.handledMounts)
	}

	data, err := os.ReadFile(receivedOptionsPath)
	if err != nil {
		t.Fatalf("fake shim did not record stdin: %v", err)
	}
	var any types.Any
	if err := proto.Unmarshal(data, &any); err != nil {
		t.Fatalf("stdin sent to shim is not a valid Any: %v", err)
	}
	v, err := typeurl.UnmarshalAny(&any)
	if err != nil {
		t.Fatal(err)
	}
	received, ok := v.(*runcoptions.Options)
	if !ok {
		t.Fatalf("expected *options.Options, got %T", v)
	}
	if received.BinaryName != "/usr/local/bin/crun" {
		t.Errorf("expected BinaryName %q, got %q", "/usr/local/bin/crun", received.BinaryName)
	}
}

func TestLoadShimInfoWithoutRuntimeOptions(t *testing.T) {
	shimPath, receivedOptionsPath := setupFakeShimBinary(t, &apitypes.RuntimeInfo{Name: "fake"})

	sm := &ShimManager{}
	sinfo, err := sm.loadShimInfo(context.Background(), shimPath, nil)
	if err != nil {
		t.Fatalf("loadShimInfo failed: %v", err)
	}
	if len(sinfo.handledMounts) != 0 {
		t.Errorf("expected no handled mounts, got %v", sinfo.handledMounts)
	}

	data, err := os.ReadFile(receivedOptionsPath)
	if err != nil {
		t.Fatalf("fake shim did not record stdin: %v", err)
	}
	if len(data) != 0 {
		t.Errorf("expected empty stdin, got %d bytes", len(data))
	}
}

func TestLoadShimInfoCachesResult(t *testing.T) {
	shimPath, receivedOptionsPath := setupFakeShimBinary(t, &apitypes.RuntimeInfo{Name: "fake"})

	sm := &ShimManager{}
	if _, err := sm.loadShimInfo(context.Background(), shimPath, nil); err != nil {
		t.Fatalf("loadShimInfo failed: %v", err)
	}
	if err := os.Remove(receivedOptionsPath); err != nil {
		t.Fatal(err)
	}
	if _, err := sm.loadShimInfo(context.Background(), shimPath, nil); err != nil {
		t.Fatalf("second loadShimInfo failed: %v", err)
	}
	if _, err := os.Stat(receivedOptionsPath); !os.IsNotExist(err) {
		t.Errorf("expected cached result, but the shim was executed again")
	}
}
