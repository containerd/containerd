package main

import (
	gocontext "context"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/containerd/containerd/api/services/execution"
	"github.com/crosbymichael/console"
	protobuf "github.com/gogo/protobuf/types"
	specs "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/urfave/cli"
)

var execCommand = cli.Command{
	Name:  "exec",
	Usage: "execute additional processes in an existing container",
	Flags: []cli.Flag{
		cli.StringFlag{
			Name:  "id",
			Usage: "id of the container",
		},
		cli.BoolFlag{
			Name:  "tty,t",
			Usage: "allocate a TTY for the container",
		},
	},
	Action: func(context *cli.Context) error {
		var (
			id  = context.String("id")
			ctx = gocontext.Background()
		)

		process := createProcess(context.Args(), "", context.Bool("tty"))
		data, err := json.Marshal(process)
		if err != nil {
			return err
		}
		containers, err := getExecutionService(context)
		if err != nil {
			return err
		}
		events, err := containers.Events(ctx, &execution.EventsRequest{})
		if err != nil {
			return err
		}
		tmpDir, err := getTempDir(id)
		if err != nil {
			return err
		}
		defer os.RemoveAll(tmpDir)
		request := &execution.ExecRequest{
			ID: id,
			Spec: &protobuf.Any{
				TypeUrl: specs.Version,
				Value:   data,
			},
			Terminal: context.Bool("tty"),
			Stdin:    filepath.Join(tmpDir, "stdin"),
			Stdout:   filepath.Join(tmpDir, "stdout"),
			Stderr:   filepath.Join(tmpDir, "stderr"),
		}
		if request.Terminal {
			con := console.Current()
			defer con.Reset()
			if err := con.SetRaw(); err != nil {
				return err
			}
		}
		fwg, err := prepareStdio(request.Stdin, request.Stdout, request.Stderr, request.Terminal)
		if err != nil {
			return err
		}
		response, err := containers.Exec(ctx, request)
		if err != nil {
			return err
		}
		// Ensure we read all io only if container started successfully.
		defer fwg.Wait()

		status, err := waitContainer(events, id, response.Pid)
		if err != nil {
			return err
		}
		if status != 0 {
			return cli.NewExitError("", int(status))
		}
		return nil
	},
}

func createProcess(args []string, cwd string, tty bool) specs.Process {
	env := []string{
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
	}
	if tty {
		env = append(env, "TERM=xterm")
	}
	if cwd == "" {
		cwd = "/"
	}
	return specs.Process{
		Args:            args,
		Env:             env,
		Terminal:        tty,
		Cwd:             cwd,
		NoNewPrivileges: true,
		User: specs.User{
			UID: 0,
			GID: 0,
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
	}
}
