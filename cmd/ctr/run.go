package main

import (
	gocontext "context"
	"encoding/json"
	"io/ioutil"
	"path/filepath"
	"runtime"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"

	"github.com/Sirupsen/logrus"
	"github.com/crosbymichael/console"
	"github.com/docker/containerd/api/services/execution"
	rootfsapi "github.com/docker/containerd/api/services/rootfs"
	"github.com/docker/containerd/image"
	protobuf "github.com/gogo/protobuf/types"
	"github.com/opencontainers/image-spec/identity"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/pkg/errors"
	"github.com/urfave/cli"
)

var rwm = "rwm"

const rootfsPath = "rootfs"

func spec(id string, args []string, tty bool) *specs.Spec {
	return &specs.Spec{
		Version: specs.Version,
		Platform: specs.Platform{
			OS:   runtime.GOOS,
			Arch: runtime.GOARCH,
		},
		Root: specs.Root{
			Path:     rootfsPath,
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

func customSpec(configPath string) (*specs.Spec, error) {
	b, err := ioutil.ReadFile(configPath)
	if err != nil {
		return nil, err
	}
	var s specs.Spec
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, err
	}
	if s.Root.Path != rootfsPath {
		logrus.Warnf("ignoring Root.Path %q, setting %q forcibly", s.Root.Path, rootfsPath)
		s.Root.Path = rootfsPath
	}
	return &s, nil
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
		cli.StringFlag{
			Name:  "runtime-config",
			Usage: "custom runtime config (config.json)",
		},
		cli.StringFlag{
			Name:  "runtime",
			Usage: "runtime name (linux, windows, vmware-linux)",
			Value: "linux",
		},
	},
	Action: func(context *cli.Context) error {
		ctx := gocontext.Background()
		id := context.String("id")
		if id == "" {
			return errors.New("container id must be provided")
		}

		containers, err := getExecutionService(context)
		if err != nil {
			return err
		}
		tmpDir, err := getTempDir(id)
		if err != nil {
			return err
		}
		events, err := containers.Events(ctx, &execution.EventsRequest{})
		if err != nil {
			return err
		}

		provider, err := getContentProvider(context)
		if err != nil {
			return err
		}

		rootfsClient, err := getRootFSService(context)
		if err != nil {
			return err
		}

		db, err := getDB(context, false)
		if err != nil {
			return errors.Wrap(err, "failed opening database")
		}
		defer db.Close()

		tx, err := db.Begin(false)
		if err != nil {
			return err
		}
		defer tx.Rollback()

		ref := context.Args().First()

		im, err := image.Get(tx, ref)
		if err != nil {
			return errors.Wrapf(err, "could not resolve %q", ref)
		}
		// let's close out our db and tx so we don't hold the lock whilst running.
		tx.Rollback()
		db.Close()

		diffIDs, err := im.RootFS(ctx, provider)
		if err != nil {
			return err
		}

		if _, err := rootfsClient.Prepare(gocontext.TODO(), &rootfsapi.PrepareRequest{
			Name:    id,
			ChainID: identity.ChainID(diffIDs),
		}); err != nil {
			if grpc.Code(err) != codes.AlreadyExists {
				return err
			}
		}

		resp, err := rootfsClient.Mounts(gocontext.TODO(), &rootfsapi.MountsRequest{
			Name: id,
		})
		if err != nil {
			return err
		}

		rootfs := resp.Mounts

		var s *specs.Spec
		if config := context.String("runtime-config"); config == "" {
			s = spec(id, []string(context.Args().Tail()), context.Bool("tty"))
		} else {
			s, err = customSpec(config)
			if err != nil {
				return err
			}
		}
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
			Runtime:  context.String("runtime"),
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

		// Ensure we read all io only if container started successfully.
		defer fwg.Wait()

		status, err := waitContainer(events, response)
		if err != nil {
			return err
		}
		if _, err := containers.Delete(gocontext.Background(), &execution.DeleteRequest{
			ID: response.ID,
		}); err != nil {
			return err
		}
		if status != 0 {
			return cli.NewExitError("", int(status))
		}
		return nil
	},
}
