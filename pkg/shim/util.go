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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/containerd/ttrpc"
	"github.com/containerd/typeurl/v2"

	bootapi "github.com/containerd/containerd/api/runtime/boot/v1"
	"github.com/containerd/containerd/v2/pkg/atomicfile"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/containerd/containerd/v2/pkg/protobuf/proto"
	"github.com/containerd/containerd/v2/pkg/protobuf/types"
	"github.com/containerd/errdefs"
)

type CommandConfig struct {
	ID           string
	RuntimePath  string
	BundlePath   string
	GRPCAddress  string
	TTRPCAddress string
	WorkDir      string
	Args         []string
	Opts         *types.Any
	Env          []string
	Debug        bool
	Action       string // Either "start" or "delete"
}

// Command returns the shim command with the provided args and configuration
//
// TODO(refactor): move this function to core containerd runtime. This function is only used
// by the containerd daemon for the initial shim launches (e.g. shim -start) and makes
// no sense for shim implementations.
func Command(ctx context.Context, config *CommandConfig) (*exec.Cmd, error) {
	ns, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return nil, err
	}
	self, err := os.Executable()
	if err != nil {
		return nil, err
	}

	// TODO: Deprecate this in 2.3 and remove in the next LTS in favor of Bootstrap protocol.
	args := []string{
		"-namespace", ns,
		"-address", config.GRPCAddress,
		"-publish-binary", self,
		"-id", config.ID,
	}
	if config.BundlePath != "" {
		args = append(args, "-bundle", config.BundlePath)
	}
	if config.Debug {
		args = append(args, "-debug")
	}
	if config.Action == "" {
		return nil, errors.New("action must be specified in CommandConfig")
	}

	args = append(args, config.Action)

	if len(config.Args) > 0 {
		args = append(args, config.Args...)
	}

	cmd := exec.CommandContext(ctx, config.RuntimePath, args...)
	cmd.Dir = config.WorkDir
	cmd.Env = append(
		os.Environ(),
		"GOMAXPROCS=2",
		fmt.Sprintf("%s=2", maxVersionEnv),
		// TODO: Deprecate this in 2.3 and remove in the next LTS in favor of Bootstrap protocol.
		fmt.Sprintf("%s=%s", ttrpcAddressEnv, config.TTRPCAddress),
		fmt.Sprintf("%s=%s", grpcAddressEnv, config.GRPCAddress),
		fmt.Sprintf("%s=%s", namespaceEnv, ns),
	)
	if len(config.Env) > 0 {
		cmd.Env = append(cmd.Env, config.Env...)
	}
	cmd.SysProcAttr = getSysProcAttr()

	// Special path when upgrading from 1.7 runc v1 shims to 2.x containerd.
	// v1 shims would fail if passed wrong stdin data.
	// TODO: Deprecate and remove in the next LTS
	if strings.Contains(config.RuntimePath, "shim-runc-v1") {
		if config.Opts != nil {
			d, err := proto.Marshal(config.Opts)
			if err != nil {
				return nil, err
			}
			cmd.Stdin = bytes.NewReader(d)
		}
	} else if config.Action == "start" {
		// Use the new Bootstrap protocol for all newer shims.
		params := bootapi.BootstrapParams{
			ID:                     config.ID,
			Namespace:              ns,
			EnableDebug:            config.Debug,
			ContainerdGrpcAddress:  config.GRPCAddress,
			ContainerdTtrpcAddress: config.TTRPCAddress,
			ContainerdBinary:       self,
		}

		if config.Opts != nil {
			if err := params.AddExtension(config.Opts); err != nil {
				return nil, fmt.Errorf("unable to add runtime options extensions: %w", err)
			}
		}

		data, err := json.Marshal(&params)
		if err != nil {
			return nil, fmt.Errorf("unable to marshal bootstrap params: %w", err)
		}

		cmd.Stdin = bytes.NewReader(data)
	}

	return cmd, nil
}

// BinaryName returns the shim binary name from the runtime name,
// empty string returns means runtime name is invalid
func BinaryName(runtime string) string {
	// runtime name should format like $prefix.name.version
	parts := strings.Split(runtime, ".")
	if len(parts) < 2 || parts[0] == "" {
		return ""
	}

	return fmt.Sprintf(shimBinaryFormat, parts[len(parts)-2], parts[len(parts)-1])
}

// BinaryPath returns the full path for the shim binary from the runtime name,
// empty string returns means runtime name is invalid
func BinaryPath(runtime string) string {
	dir := filepath.Dir(runtime)
	binary := BinaryName(runtime)

	path, err := filepath.Abs(filepath.Join(dir, binary))
	if err != nil {
		return ""
	}

	return path
}

// Connect to the provided address
func Connect(address string, d func(string, time.Duration) (net.Conn, error)) (net.Conn, error) {
	return d(address, 100*time.Second)
}

// WritePidFile writes a pid file atomically
func WritePidFile(path string, pid int) error {
	path, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	f, err := atomicfile.New(path, 0o644)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(f, "%d", pid)
	if err != nil {
		f.Cancel()
		return err
	}
	return f.Close()
}

// ErrNoAddress is returned when the address file has no content
var ErrNoAddress = errors.New("no shim address")

// ReadAddress returns the shim's socket address from the path
func ReadAddress(path string) (string, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if len(data) == 0 {
		return "", ErrNoAddress
	}
	return string(data), nil
}

// ReadRuntimeOptions reads config bytes from io.Reader and unmarshals it into the provided type.
// The type must be registered with typeurl.
//
// The function will return ErrNotFound, if the config is not provided.
// And ErrInvalidArgument, if unable to cast the config to the provided type T.
func ReadRuntimeOptions[T any](reader io.Reader) (T, error) {
	var config T

	data, err := io.ReadAll(reader)
	if err != nil {
		return config, fmt.Errorf("failed to read config bytes from stdin: %w", err)
	}

	if len(data) == 0 {
		return config, errdefs.ErrNotFound
	}

	var any types.Any
	if err := proto.Unmarshal(data, &any); err != nil {
		return config, err
	}

	v, err := typeurl.UnmarshalAny(&any)
	if err != nil {
		return config, err
	}

	config, ok := v.(T)
	if !ok {
		return config, fmt.Errorf("invalid type %T: %w", v, errdefs.ErrInvalidArgument)
	}

	return config, nil
}

// chainUnaryServerInterceptors creates a single ttrpc server interceptor from
// a chain of many interceptors executed from first to last.
func chainUnaryServerInterceptors(interceptors ...ttrpc.UnaryServerInterceptor) ttrpc.UnaryServerInterceptor {
	n := len(interceptors)

	// force to use default interceptor in ttrpc
	if n == 0 {
		return nil
	}

	return func(ctx context.Context, unmarshal ttrpc.Unmarshaler, info *ttrpc.UnaryServerInfo, method ttrpc.Method) (interface{}, error) {
		currentMethod := method

		for i := n - 1; i > 0; i-- {
			interceptor := interceptors[i]
			innerMethod := currentMethod

			currentMethod = func(currentCtx context.Context, currentUnmarshal func(interface{}) error) (interface{}, error) {
				return interceptor(currentCtx, currentUnmarshal, info, innerMethod)
			}
		}
		return interceptors[0](ctx, unmarshal, info, currentMethod)
	}
}
