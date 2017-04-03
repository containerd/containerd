package main

import (
	gocontext "context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"

	"github.com/crosbymichael/console"
	"github.com/docker/containerd/api/services/execution"
	rootfsapi "github.com/docker/containerd/api/services/rootfs"
	"github.com/docker/containerd/images"
	protobuf "github.com/gogo/protobuf/types"
	"github.com/opencontainers/image-spec/identity"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/pkg/errors"
	"github.com/urfave/cli"
)

const (
	rwm        = "rwm"
	rootfsPath = "rootfs"
)

var capabilities = []string{
	"CAP_CHOWN",
	"CAP_DAC_OVERRIDE",
	"CAP_FSETID",
	"CAP_FOWNER",
	"CAP_MKNOD",
	"CAP_NET_RAW",
	"CAP_SETGID",
	"CAP_SETUID",
	"CAP_SETFCAP",
	"CAP_SETPCAP",
	"CAP_NET_BIND_SERVICE",
	"CAP_SYS_CHROOT",
	"CAP_KILL",
	"CAP_AUDIT_WRITE",
}

func spec(id string, config *ocispec.ImageConfig, context *cli.Context) (*specs.Spec, error) {
	env := []string{
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
	}
	env = append(env, config.Env...)
	cmd := config.Cmd
	if v := context.Args().Tail(); len(v) > 0 {
		cmd = v
	}
	var (
		// TODO: support overriding entrypoint
		args     = append(config.Entrypoint, cmd...)
		tty      = context.Bool("tty")
		uid, gid uint32
	)
	if config.User != "" {
		parts := strings.Split(config.User, ":")
		switch len(parts) {
		case 1:
			v, err := strconv.ParseUint(parts[0], 0, 10)
			if err != nil {
				return nil, err
			}
			uid, gid = uint32(v), uint32(v)
		case 2:
			v, err := strconv.ParseUint(parts[0], 0, 10)
			if err != nil {
				return nil, err
			}
			uid = uint32(v)
			if v, err = strconv.ParseUint(parts[1], 0, 10); err != nil {
				return nil, err
			}
			gid = uint32(v)
		default:
			return nil, fmt.Errorf("invalid USER value %s", config.User)
		}
	}
	if tty {
		env = append(env, "TERM=xterm")
	}
	cwd := config.WorkingDir
	if cwd == "" {
		cwd = "/"
	}
	return &specs.Spec{
		Version: specs.Version,
		Platform: specs.Platform{
			OS:   runtime.GOOS,
			Arch: runtime.GOARCH,
		},
		Root: specs.Root{
			Path:     rootfsPath,
			Readonly: context.Bool("readonly"),
		},
		Process: specs.Process{
			Args:            args,
			Env:             env,
			Terminal:        tty,
			Cwd:             cwd,
			NoNewPrivileges: true,
			User: specs.User{
				UID: uid,
				GID: gid,
			},
			Capabilities: &specs.LinuxCapabilities{
				Bounding:    capabilities,
				Permitted:   capabilities,
				Inheritable: capabilities,
				Effective:   capabilities,
				Ambient:     capabilities,
			},
			Rlimits: []specs.LinuxRlimit{
				{
					Type: "RLIMIT_NOFILE",
					Hard: uint64(1024),
					Soft: uint64(1024),
				},
			},
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
				Options:     []string{"nosuid", "noexec", "nodev", "ro"},
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
						Access: rwm,
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
	}, nil
}

var runCommand = cli.Command{
	Name:      "run",
	Usage:     "run a container",
	ArgsUsage: "IMAGE [COMMAND] [ARG...]",
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
			Name:  "runtime",
			Usage: "runtime name (linux, windows, vmware-linux)",
			Value: "linux",
		},
		cli.BoolFlag{
			Name:  "readonly",
			Usage: "set the containers filesystem as readonly",
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
		defer os.RemoveAll(tmpDir)
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

		image, err := images.Get(tx, ref)
		if err != nil {
			return errors.Wrapf(err, "could not resolve %q", ref)
		}
		// let's close out our db and tx so we don't hold the lock whilst running.
		tx.Rollback()
		db.Close()

		diffIDs, err := image.RootFS(ctx, provider)
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

		ic, err := image.Config(ctx, provider)
		if err != nil {
			return err
		}
		var imageConfig ocispec.Image
		switch ic.MediaType {
		case ocispec.MediaTypeImageConfig, images.MediaTypeDockerSchema2Config:
			r, err := provider.Reader(ctx, ic.Digest)
			if err != nil {
				return err
			}
			if err := json.NewDecoder(r).Decode(&imageConfig); err != nil {
				r.Close()
				return err
			}
			r.Close()
		default:
			return fmt.Errorf("unknown image config media type %s", ic.MediaType)
		}
		rootfs := resp.Mounts
		// generate the spec based on the image config
		s, err := spec(id, &imageConfig.Config, context)
		if err != nil {
			return err
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
