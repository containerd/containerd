package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"runtime"

	"github.com/Sirupsen/logrus"
	"github.com/containerd/containerd/api/services/execution"
	"github.com/containerd/containerd/windows"
	"github.com/containerd/containerd/windows/hcs"
	protobuf "github.com/gogo/protobuf/types"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	specs "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/urfave/cli"
)

const pipeRoot = `\\.\pipe`

func init() {
	runCommand.Flags = append(runCommand.Flags, cli.StringSliceFlag{
		Name:  "layers",
		Usage: "HCSSHIM Layers to be used",
	})
}

func spec(id string, config *ocispec.ImageConfig, context *cli.Context) *specs.Spec {
	cmd := config.Cmd
	if a := context.Args().First(); a != "" {
		cmd = context.Args()
	}

	var (
		// TODO: support overriding entrypoint
		args = append(config.Entrypoint, cmd...)
		tty  = context.Bool("tty")
		cwd  = config.WorkingDir
	)

	if cwd == "" {
		cwd = `C:\`
	}

	return &specs.Spec{
		Version: specs.Version,
		Platform: specs.Platform{
			OS:   runtime.GOOS,
			Arch: runtime.GOARCH,
		},
		Root: specs.Root{
			Readonly: context.Bool("readonly"),
		},
		Process: specs.Process{
			Args:     args,
			Terminal: tty,
			Cwd:      cwd,
			Env:      config.Env,
			User: specs.User{
				Username: config.User,
			},
			ConsoleSize: specs.Box{
				Height: 20,
				Width:  80,
			},
		},
		Hostname: id,
	}
}

func customSpec(context *cli.Context, configPath string) (*specs.Spec, error) {
	b, err := ioutil.ReadFile(configPath)
	if err != nil {
		return nil, err
	}

	var s specs.Spec
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, err
	}

	rootfs := context.String("rootfs")
	if rootfs != "" && s.Root.Path != rootfs {
		logrus.Warnf("ignoring config Root.Path %q, setting %q forcibly", s.Root.Path, rootfs)
		s.Root.Path = rootfs
	}
	return &s, nil
}

func getConfig(context *cli.Context, imageConfig *ocispec.ImageConfig) (*specs.Spec, error) {
	if config := context.String("runtime-config"); config != "" {
		return customSpec(context, config)
	}

	s := spec(context.String("id"), imageConfig, context)
	if rootfs := context.String("rootfs"); rootfs != "" {
		s.Root.Path = rootfs
	}

	return s, nil
}

func newCreateRequest(context *cli.Context, imageConfig *ocispec.ImageConfig, id, tmpDir string) (*execution.CreateRequest, error) {
	spec, err := getConfig(context, imageConfig)
	if err != nil {
		return nil, err
	}

	rtSpec := windows.RuntimeSpec{
		OCISpec: *spec,
		Configuration: hcs.Configuration{
			Layers:                   context.StringSlice("layers"),
			IgnoreFlushesDuringBoot:  true,
			AllowUnqualifiedDNSQuery: true},
	}

	data, err := json.Marshal(rtSpec)
	if err != nil {
		return nil, err
	}
	create := &execution.CreateRequest{
		ID: id,
		Spec: &protobuf.Any{
			TypeUrl: specs.Version,
			Value:   data,
		},
		Runtime:  context.String("runtime"),
		Terminal: context.Bool("tty"),
		Stdin:    fmt.Sprintf(`%s\ctr-%s-stdin`, pipeRoot, id),
		Stdout:   fmt.Sprintf(`%s\ctr-%s-stdout`, pipeRoot, id),
	}
	if !create.Terminal {
		create.Stderr = fmt.Sprintf(`%s\ctr-%s-stderr`, pipeRoot, id)
	}

	return create, nil
}
