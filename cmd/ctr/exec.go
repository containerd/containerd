package main

import (
	"os"
	"path/filepath"
	"time"

	gocontext "context"

	"github.com/docker/containerd/api/services/execution"
	"github.com/urfave/cli"
)

var execCommand = cli.Command{
	Name:  "exec",
	Usage: "exec a new process in a running container",
	Flags: []cli.Flag{
		cli.StringFlag{
			Name:  "id, i",
			Usage: "target container id",
		},
		cli.StringFlag{
			Name:  "cwd, c",
			Usage: "current working directory for the process",
		},
		cli.BoolFlag{
			Name:  "tty, t",
			Usage: "create a terminal for the process",
		},
		cli.StringSliceFlag{
			Name:  "env, e",
			Value: &cli.StringSlice{},
			Usage: "environment variables for the process",
		},
	},
	Action: func(context *cli.Context) error {
		executionService, err := getExecutionService(context)
		if err != nil {
			return err
		}

		tmpDir, err := getTempDir(time.Now().Format("2006-02-01_15:04:05"))
		if err != nil {
			return err
		}
		defer os.RemoveAll(tmpDir)

		id := context.String("id")
		sOpts := &execution.StartProcessRequest{
			ContainerID: id,
			Process: &execution.Process{
				Cwd:      context.String("cwd"),
				Terminal: context.Bool("tty"),
				Args:     context.Args(),
				Env:      context.StringSlice("env"),
			},
			Stdin:   filepath.Join(tmpDir, "stdin"),
			Stdout:  filepath.Join(tmpDir, "stdout"),
			Stderr:  filepath.Join(tmpDir, "stderr"),
			Console: context.Bool("tty"),
		}

		fwg, err := prepareStdio(sOpts.Stdin, sOpts.Stdout, sOpts.Stderr, sOpts.Console)
		if err != nil {
			return err
		}

		sr, err := executionService.StartProcess(gocontext.Background(), sOpts)
		if err != nil {
			return err
		}

		_, err = executionService.DeleteProcess(gocontext.Background(), &execution.DeleteProcessRequest{
			ContainerID: id,
			Pid:         sr.Pid,
		})
		if err != nil {
			return err
		}

		// Ensure we read all io
		fwg.Wait()

		return nil
	},
}
