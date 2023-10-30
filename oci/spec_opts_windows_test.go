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
	"os"
	"testing"

	"github.com/containerd/containerd/v2/containers"
	"github.com/containerd/containerd/v2/namespaces"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/opencontainers/runtime-spec/specs-go"
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

// TestWithProcessArgsOverwritesWithImage verifies that when calling
// WithImageConfig followed by WithProcessArgs when `ArgsEscaped==false` that
// the process args overwrite the image args.
func TestWithProcessArgsOverwritesWithImage(t *testing.T) {
	t.Parallel()

	img, err := newFakeImage(ocispec.Image{
		Config: ocispec.ImageConfig{
			Entrypoint:  []string{"powershell.exe", "-Command", "Write-Host Hello"},
			Cmd:         []string{"cmd.exe", "/S", "/C", "echo Hello"},
			ArgsEscaped: false,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	s := Spec{
		Version: specs.Version,
		Root:    &specs.Root{},
		Windows: &specs.Windows{},
	}

	args := []string{"cmd.exe", "echo", "should be set"}
	opts := []SpecOpts{
		WithImageConfig(img),
		WithProcessArgs(args...),
	}

	for _, opt := range opts {
		if err := opt(nil, nil, nil, &s); err != nil {
			t.Fatal(err)
		}
	}

	if err := assertEqualsStringArrays(args, s.Process.Args); err != nil {
		t.Fatal(err)
	}
	if s.Process.CommandLine != "" {
		t.Fatalf("Expected empty CommandLine, got: '%s'", s.Process.CommandLine)
	}
}

// TestWithProcessArgsOverwritesWithImageArgsEscaped verifies that when calling
// WithImageConfig followed by WithProcessArgs when `ArgsEscaped==true` that the
// process args overwrite the image args.
func TestWithProcessArgsOverwritesWithImageArgsEscaped(t *testing.T) {
	t.Parallel()

	img, err := newFakeImage(ocispec.Image{
		Config: ocispec.ImageConfig{
			Entrypoint:  []string{`powershell.exe -Command "C:\My Data\MyExe.exe" -arg1 "-arg2 value2"`},
			Cmd:         []string{`cmd.exe /S /C "C:\test path\test.exe"`},
			ArgsEscaped: true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	s := Spec{
		Version: specs.Version,
		Root:    &specs.Root{},
		Windows: &specs.Windows{},
	}

	args := []string{"cmd.exe", "echo", "should be set"}
	opts := []SpecOpts{
		WithImageConfig(img),
		WithProcessArgs(args...),
	}

	for _, opt := range opts {
		if err := opt(nil, nil, nil, &s); err != nil {
			t.Fatal(err)
		}
	}

	if err := assertEqualsStringArrays(args, s.Process.Args); err != nil {
		t.Fatal(err)
	}
	if s.Process.CommandLine != "" {
		t.Fatalf("Expected empty CommandLine, got: '%s'", s.Process.CommandLine)
	}
}

// TestWithImageOverwritesWithProcessArgs verifies that when calling
// WithProcessArgs followed by WithImageConfig `ArgsEscaped==false` that the
// image args overwrites process args.
func TestWithImageOverwritesWithProcessArgs(t *testing.T) {
	t.Parallel()

	img, err := newFakeImage(ocispec.Image{
		Config: ocispec.ImageConfig{
			Entrypoint: []string{"powershell.exe", "-Command"},
			Cmd:        []string{"Write-Host", "echo Hello"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	s := Spec{
		Version: specs.Version,
		Root:    &specs.Root{},
		Windows: &specs.Windows{},
	}

	opts := []SpecOpts{
		WithProcessArgs("cmd.exe", "echo", "should not be set"),
		WithImageConfig(img),
	}

	for _, opt := range opts {
		if err := opt(nil, nil, nil, &s); err != nil {
			t.Fatal(err)
		}
	}

	expectedArgs := []string{"powershell.exe", "-Command", "Write-Host", "echo Hello"}
	if err := assertEqualsStringArrays(expectedArgs, s.Process.Args); err != nil {
		t.Fatal(err)
	}
	if s.Process.CommandLine != "" {
		t.Fatalf("Expected empty CommandLine, got: '%s'", s.Process.CommandLine)
	}
}

// TestWithImageOverwritesWithProcessArgs verifies that when calling
// WithProcessArgs followed by WithImageConfig `ArgsEscaped==true` that the
// image args overwrites process args.
func TestWithImageArgsEscapedOverwritesWithProcessArgs(t *testing.T) {
	t.Parallel()

	img, err := newFakeImage(ocispec.Image{
		Config: ocispec.ImageConfig{
			Entrypoint:  []string{`powershell.exe -Command "C:\My Data\MyExe.exe" -arg1 "-arg2 value2"`},
			Cmd:         []string{`cmd.exe /S /C "C:\test path\test.exe"`},
			ArgsEscaped: true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	s := Spec{
		Version: specs.Version,
		Root:    &specs.Root{},
		Windows: &specs.Windows{},
	}

	opts := []SpecOpts{
		WithProcessArgs("cmd.exe", "echo", "should not be set"),
		WithImageConfig(img),
	}

	expectedCommandLine := `powershell.exe -Command "C:\My Data\MyExe.exe" -arg1 "-arg2 value2" "cmd.exe /S /C \"C:\test path\test.exe\""`

	for _, opt := range opts {
		if err := opt(nil, nil, nil, &s); err != nil {
			t.Fatal(err)
		}
	}

	if s.Process.Args != nil {
		t.Fatalf("Expected empty Process.Args, got: '%v'", s.Process.Args)
	}
	if expectedCommandLine != s.Process.CommandLine {
		t.Fatalf("Expected CommandLine '%s', got: '%s'", expectedCommandLine, s.Process.CommandLine)
	}
}

func TestWithImageConfigArgsWindows(t *testing.T) {
	testcases := []struct {
		name       string
		entrypoint []string
		cmd        []string
		args       []string

		expectError bool
		// When ArgsEscaped==false we always expect args and CommandLine==""
		expectedArgs []string
	}{
		{
			// This is not really a valid test case since Docker would have made
			// the default cmd to be the shell. So just verify it hits the error
			// case we expect.
			name:        "EmptyEntrypoint_EmptyCmd_EmptyArgs",
			entrypoint:  nil,
			cmd:         nil,
			args:        nil,
			expectError: true,
		},
		{
			name:         "EmptyEntrypoint_EmptyCmd_Args",
			entrypoint:   nil,
			cmd:          nil,
			args:         []string{"additional", "args"},
			expectedArgs: []string{"additional", "args"},
		},
		{
			name:         "EmptyEntrypoint_Cmd_EmptyArgs",
			entrypoint:   nil,
			cmd:          []string{"cmd", "args"},
			args:         nil,
			expectedArgs: []string{"cmd", "args"},
		},
		{
			name:         "EmptyEntrypoint_Cmd_Args",
			entrypoint:   nil,
			cmd:          []string{"cmd", "args"},
			args:         []string{"additional", "args"},
			expectedArgs: []string{"additional", "args"}, // Args overwrite Cmd
		},
		{
			name:         "Entrypoint_EmptyCmd_EmptyArgs",
			entrypoint:   []string{"entrypoint", "args"},
			cmd:          nil,
			args:         nil,
			expectedArgs: []string{"entrypoint", "args"},
		},
		{
			name:         "Entrypoint_EmptyCmd_Args",
			entrypoint:   []string{"entrypoint", "args"},
			cmd:          nil,
			args:         []string{"additional", "args"},
			expectedArgs: []string{"entrypoint", "args", "additional", "args"},
		},
		{
			name:         "Entrypoint_Cmd_EmptyArgs",
			entrypoint:   []string{"entrypoint", "args"},
			cmd:          []string{"cmd", "args"},
			args:         nil,
			expectedArgs: []string{"entrypoint", "args", "cmd", "args"},
		},
		{
			name:         "Entrypoint_Cmd_Args",
			entrypoint:   []string{"entrypoint", "args"},
			cmd:          []string{"cmd", "args"},
			args:         []string{"additional", "args"}, // Args overwrites Cmd
			expectedArgs: []string{"entrypoint", "args", "additional", "args"},
		},
	}
	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			img, err := newFakeImage(ocispec.Image{
				Config: ocispec.ImageConfig{
					Entrypoint: tc.entrypoint,
					Cmd:        tc.cmd,
				},
			})
			if err != nil {
				t.Fatal(err)
			}

			s := Spec{
				Version: specs.Version,
				Root:    &specs.Root{},
				Windows: &specs.Windows{},
			}

			opts := []SpecOpts{
				WithImageConfigArgs(img, tc.args),
			}

			for _, opt := range opts {
				if err := opt(nil, nil, nil, &s); err != nil {
					if tc.expectError {
						continue
					}
					t.Fatal(err)
				}
			}

			if err := assertEqualsStringArrays(tc.expectedArgs, s.Process.Args); err != nil {
				t.Fatal(err)
			}
			if s.Process.CommandLine != "" {
				t.Fatalf("Expected empty CommandLine, got: '%s'", s.Process.CommandLine)
			}
		})
	}
}

func TestWithImageConfigArgsEscapedWindows(t *testing.T) {
	testcases := []struct {
		name       string
		entrypoint []string
		cmd        []string
		args       []string

		expectError         bool
		expectedArgs        []string
		expectedCommandLine string
	}{
		{
			// This is not really a valid test case since Docker would have made
			// the default cmd to be the shell. So just verify it hits the error
			// case we expect.
			name:                "EmptyEntrypoint_EmptyCmd_EmptyArgs",
			entrypoint:          nil,
			cmd:                 nil,
			args:                nil,
			expectError:         true,
			expectedArgs:        nil,
			expectedCommandLine: "",
		},
		{
			// This case is special for ArgsEscaped, since there is no Image
			// Default Args should be passed as ProcessArgs not as Cmdline
			name:                "EmptyEntrypoint_EmptyCmd_Args",
			entrypoint:          nil,
			cmd:                 nil,
			args:                []string{"additional", "-args", "hello world"},
			expectedArgs:        []string{"additional", "-args", "hello world"},
			expectedCommandLine: "",
		},
		{
			name:                "EmptyEntrypoint_Cmd_EmptyArgs",
			entrypoint:          nil,
			cmd:                 []string{`cmd -args "hello world"`},
			args:                nil,
			expectedCommandLine: `cmd -args "hello world"`,
		},
		{
			// This case is a second special case for ArgsEscaped, since Args
			// overwrite Cmd the args are not from the image, so ArgsEscaped
			// should be ignored, and passed as Args not CommandLine.
			name:                "EmptyEntrypoint_Cmd_Args",
			entrypoint:          nil,
			cmd:                 []string{`cmd -args "hello world"`},
			args:                []string{"additional", "args"},
			expectedArgs:        []string{"additional", "args"}, // Args overwrite Cmd
			expectedCommandLine: "",
		},
		{
			name:                "Entrypoint_EmptyCmd_EmptyArgs",
			entrypoint:          []string{`"C:\My Folder\MyProcess.exe" -arg1 "test value"`},
			cmd:                 nil,
			args:                nil,
			expectedCommandLine: `"C:\My Folder\MyProcess.exe" -arg1 "test value"`,
		},
		{
			name:                "Entrypoint_EmptyCmd_Args",
			entrypoint:          []string{`"C:\My Folder\MyProcess.exe" -arg1 "test value"`},
			cmd:                 nil,
			args:                []string{"additional", "args with spaces"},
			expectedCommandLine: `"C:\My Folder\MyProcess.exe" -arg1 "test value" additional "args with spaces"`,
		},
		{
			// This case will not work in Docker today so adding the test to
			// confirm we fail in the same way. Although the appending of
			// Entrypoint + " " + Cmd here works, Cmd is double escaped and the
			// container would not launch. This is because when Docker built
			// such an image it escaped both Entrypoint and Cmd. However the
			// docs say that CMD should always be appened to entrypoint if not
			// overwritten so this results in an incorrect cmdline.
			name:                "Entrypoint_Cmd_EmptyArgs",
			entrypoint:          []string{`"C:\My Folder\MyProcess.exe" -arg1 "test value"`},
			cmd:                 []string{`cmd -args "hello world"`},
			args:                nil,
			expectedCommandLine: `"C:\My Folder\MyProcess.exe" -arg1 "test value" "cmd -args \"hello world\""`,
		},
		{
			name:                "Entrypoint_Cmd_Args",
			entrypoint:          []string{`"C:\My Folder\MyProcess.exe" -arg1 "test value"`},
			cmd:                 []string{`cmd -args "hello world"`},
			args:                []string{"additional", "args with spaces"}, // Args overwrites Cmd
			expectedCommandLine: `"C:\My Folder\MyProcess.exe" -arg1 "test value" additional "args with spaces"`,
		},
	}
	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			img, err := newFakeImage(ocispec.Image{
				Config: ocispec.ImageConfig{
					Entrypoint:  tc.entrypoint,
					Cmd:         tc.cmd,
					ArgsEscaped: true,
				},
			})
			if err != nil {
				t.Fatal(err)
			}

			s := Spec{
				Version: specs.Version,
				Root:    &specs.Root{},
				Windows: &specs.Windows{},
			}

			opts := []SpecOpts{
				WithImageConfigArgs(img, tc.args),
			}

			for _, opt := range opts {
				if err := opt(nil, nil, nil, &s); err != nil {
					if tc.expectError {
						continue
					}
					t.Fatal(err)
				}
			}

			if err := assertEqualsStringArrays(tc.expectedArgs, s.Process.Args); err != nil {
				t.Fatal(err)
			}
			if tc.expectedCommandLine != s.Process.CommandLine {
				t.Fatalf("Expected CommandLine: '%s', got: '%s'", tc.expectedCommandLine, s.Process.CommandLine)
			}
		})
	}
}

func TestWindowsDefaultPathEnv(t *testing.T) {
	t.Parallel()
	s := Spec{}
	s.Process = &specs.Process{
		Env: []string{},
	}

	var (
		defaultUnixEnv = "PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
		ctx            = namespaces.WithNamespace(context.Background(), "test")
	)

	//check that the default PATH environment is not null
	if os.Getenv("PATH") == "" {
		t.Fatal("PATH environment variable is not set")
	}
	WithDefaultPathEnv(ctx, nil, nil, &s)
	//check that the path is not overwritten by the unix default path
	if Contains(s.Process.Env, defaultUnixEnv) {
		t.Fatal("default Windows Env overwritten by the default Unix Env")
	}
}
