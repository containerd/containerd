package main

import (
	"encoding/json"
	"fmt"
	"path/filepath"

	gocontext "context"

	"runtime"

	"github.com/crosbymichael/console"
	"github.com/docker/containerd/api/services/execution"
	"github.com/docker/containerd/api/types/mount"
	protobuf "github.com/gogo/protobuf/types"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/urfave/cli"
)

var rwm = "rwm"

func spec(id string, args []string, tty bool) *specs.Spec {
	return &specs.Spec{
		Version: specs.Version,
		Platform: specs.Platform{
			OS:   runtime.GOOS,
			Arch: runtime.GOARCH,
		},
		Root: specs.Root{
			Path:     "rootfs",
			Readonly: true,
		},
		Process: specs.Process{
			Args: args,
			Env: []string{
				"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
			},
			Terminal:        tty,
			Cwd:             "/",
			NoNewPrivileges: true,
		},
		Mounts: []specs.Mount{
			{
				Destination: "/proc",
				Type:        "proc",
				Source:      "proc",
			},
			{
				Destination: "/dev",
				Type:        "tmpfs",
				Source:      "tmpfs",
				Options:     []string{"nosuid", "strictatime", "mode=755", "size=65536k"},
			},
			{
				Destination: "/dev/pts",
				Type:        "devpts",
				Source:      "devpts",
				Options:     []string{"nosuid", "noexec", "newinstance", "ptmxmode=0666", "mode=0620", "gid=5"},
			},
			{
				Destination: "/dev/shm",
				Type:        "tmpfs",
				Source:      "shm",
				Options:     []string{"nosuid", "noexec", "nodev", "mode=1777", "size=65536k"},
			},
			{
				Destination: "/dev/mqueue",
				Type:        "mqueue",
				Source:      "mqueue",
				Options:     []string{"nosuid", "noexec", "nodev"},
			},
			{
				Destination: "/sys",
				Type:        "sysfs",
				Source:      "sysfs",
				Options:     []string{"nosuid", "noexec", "nodev"},
			},
			{
				Destination: "/run",
				Type:        "tmpfs",
				Source:      "tmpfs",
				Options:     []string{"nosuid", "strictatime", "mode=755", "size=65536k"},
			},
			{
				Destination: "/etc/resolv.conf",
				Type:        "bind",
				Source:      "/etc/resolv.conf",
				Options:     []string{"rbind", "ro"},
			},
			{
				Destination: "/etc/hosts",
				Type:        "bind",
				Source:      "/etc/hosts",
				Options:     []string{"rbind", "ro"},
			},
			{
				Destination: "/etc/localtime",
				Type:        "bind",
				Source:      "/etc/localtime",
				Options:     []string{"rbind", "ro"},
			},
		},
		Hostname: id,
		Linux: &specs.Linux{
			Resources: &specs.LinuxResources{
				Devices: []specs.LinuxDeviceCgroup{
					{
						Allow:  false,
						Access: &rwm,
					},
				},
			},
			Namespaces: []specs.LinuxNamespace{
				{
					Type: "pid",
				},
				{
					Type: "ipc",
				},
				{
					Type: "uts",
				},
				{
					Type: "mount",
				},
				{
					Type: "network",
				},
			},
		},
	}
}

var runCommand = cli.Command{
	Name:  "run",
	Usage: "run a container",
	Flags: []cli.Flag{
		cli.StringFlag{
			Name:  "id",
			Usage: "id of the container",
		},
		cli.BoolFlag{
			Name:  "tty,t",
			Usage: "allocate a TTY for the container",
		},
		cli.StringFlag{
			Name:  "rootfs,r",
			Usage: "path to the container's root filesystem",
		},
	},
	Action: func(context *cli.Context) error {
		id := context.String("id")
		if id == "" {
			return fmt.Errorf("container id must be provided")
		}

		containers, err := getExecutionService(context)
		if err != nil {
			return err
		}
		tmpDir, err := getTempDir(id)
		if err != nil {
			return err
		}
		events, err := containers.Events(gocontext.Background(), &execution.EventsRequest{})
		if err != nil {
			return err
		}
		// for ctr right now just do a bind mount
		rootfs := []*mount.Mount{
			{
				Type:   "bind",
				Source: context.String("rootfs"),
				Options: []string{
					"rw",
					"rbind",
				},
			},
		}
		s := spec(id, []string(context.Args()), context.Bool("tty"))
		data, err := json.Marshal(s)
		if err != nil {
			return err
		}
		create := &execution.CreateRequest{
			ID: id,
			Spec: &protobuf.Any{
				TypeUrl: specs.Version,
				Value:   data,
			},
			Rootfs:   rootfs,
			Runtime:  "linux",
			Terminal: context.Bool("tty"),
			Stdin:    filepath.Join(tmpDir, "stdin"),
			Stdout:   filepath.Join(tmpDir, "stdout"),
			Stderr:   filepath.Join(tmpDir, "stderr"),
		}
		if create.Terminal {
			con := console.Current()
			defer con.Reset()
			if err := con.SetRaw(); err != nil {
				return err
			}
		}
		fwg, err := prepareStdio(create.Stdin, create.Stdout, create.Stderr, create.Terminal)
		if err != nil {
			return err
		}
		response, err := containers.Create(gocontext.Background(), create)
		if err != nil {
			return err
		}
		if _, err := containers.Start(gocontext.Background(), &execution.StartRequest{
			ID: response.ID,
		}); err != nil {
			return err
		}
		status, err := waitContainer(events, response)
		if err != nil {
			return err
		}
		if _, err := containers.Delete(gocontext.Background(), &execution.DeleteRequest{
			ID: response.ID,
		}); err != nil {
			return err
		}
		// Ensure we read all io
		fwg.Wait()
		if status != 0 {
			return cli.NewExitError("", int(status))
		}
		return nil
	},
}
