package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"runtime"
	"time"

	"github.com/Sirupsen/logrus"
	"github.com/containerd/console"
	containersapi "github.com/containerd/containerd/api/services/containers"
	"github.com/containerd/containerd/api/services/execution"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/mount"
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

	// Some sane defaults for console
	w := 80
	h := 20

	if tty {
		con := console.Current()
		size, err := con.Size()
		if err == nil {
			w = int(size.Width)
			h = int(size.Height)
		}
	}

	env := replaceOrAppendEnvValues(config.Env, context.StringSlice("env"))

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
			Env:      env,
			User: specs.User{
				Username: config.User,
			},
			ConsoleSize: specs.Box{
				Height: uint(w),
				Width:  uint(h),
			},
		},
		Hostname: id,
	}
}

func customSpec(context *cli.Context, configPath, rootfs string) (*specs.Spec, error) {
	b, err := ioutil.ReadFile(configPath)
	if err != nil {
		return nil, err
	}

	var s specs.Spec
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, err
	}

	if rootfs != "" && s.Root.Path != rootfs {
		logrus.Warnf("ignoring config Root.Path %q, setting %q forcibly", s.Root.Path, rootfs)
		s.Root.Path = rootfs
	}
	return &s, nil
}

func getConfig(context *cli.Context, imageConfig *ocispec.ImageConfig, rootfs string) (*specs.Spec, error) {
	if config := context.String("runtime-config"); config != "" {
		return customSpec(context, config, rootfs)
	}

	s := spec(context.String("id"), imageConfig, context)
	if rootfs != "" {
		s.Root.Path = rootfs
	}

	return s, nil
}

func newContainerSpec(context *cli.Context, config *ocispec.ImageConfig, imageRef string) ([]byte, error) {
	spec, err := getConfig(context, config, context.String("rootfs"))
	if err != nil {
		return nil, err
	}
	if spec.Annotations == nil {
		spec.Annotations = make(map[string]string)
	}
	spec.Annotations["image"] = imageRef
	rtSpec := windows.RuntimeSpec{
		OCISpec: *spec,
		Configuration: hcs.Configuration{
			Layers:                   context.StringSlice("layers"),
			IgnoreFlushesDuringBoot:  true,
			AllowUnqualifiedDNSQuery: true},
	}
	return json.Marshal(rtSpec)
}

func newCreateContainerRequest(context *cli.Context, id, snapshot string, spec []byte) (*containersapi.CreateContainerRequest, error) {
	create := &containersapi.CreateContainerRequest{
		Container: containersapi.Container{
			ID: id,
			Spec: &protobuf.Any{
				TypeUrl: specs.Version,
				Value:   spec,
			},
			Runtime: context.String("runtime"),
			RootFS:  snapshot,
		},
	}

	return create, nil
}

func newCreateTaskRequest(context *cli.Context, id, tmpDir string, checkpoint *ocispec.Descriptor, mounts []mount.Mount) (*execution.CreateRequest, error) {
	create := &execution.CreateRequest{
		ContainerID: id,
		Terminal:    context.Bool("tty"),
		Stdin:       fmt.Sprintf(`%s\ctr-%s-stdin`, pipeRoot, id),
		Stdout:      fmt.Sprintf(`%s\ctr-%s-stdout`, pipeRoot, id),
	}
	if !create.Terminal {
		create.Stderr = fmt.Sprintf(`%s\ctr-%s-stderr`, pipeRoot, id)
	}
	return create, nil
}

func handleConsoleResize(ctx context.Context, service execution.TasksClient, id string, pid uint32, con console.Console) error {
	// do an initial resize of the console
	size, err := con.Size()
	if err != nil {
		return err
	}
	go func() {
		prevSize := size
		for {
			time.Sleep(time.Millisecond * 250)

			size, err := con.Size()
			if err != nil {
				log.G(ctx).WithError(err).Error("get pty size")
				continue
			}

			if size.Width != prevSize.Width || size.Height != prevSize.Height {
				if _, err := service.Pty(ctx, &execution.PtyRequest{
					ContainerID: id,
					Pid:         pid,
					Width:       uint32(size.Width),
					Height:      uint32(size.Height),
				}); err != nil {
					log.G(ctx).WithError(err).Error("resize pty")
				}
				prevSize = size
			}
		}
	}()
	return nil
}
