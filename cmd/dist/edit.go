package main

import (
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/exec"

	contentapi "github.com/containerd/containerd/api/services/content"
	contentservice "github.com/containerd/containerd/services/content"
	digest "github.com/opencontainers/go-digest"
	"github.com/urfave/cli"
)

var editCommand = cli.Command{
	Name:        "edit",
	Usage:       "edit a blob and return a new digest.",
	ArgsUsage:   "[flags] <digest>",
	Description: `Edit a blob and return a new digest.`,
	Flags: []cli.Flag{
		cli.StringFlag{
			Name:  "validate",
			Usage: "validate the result against a format (json, mediatype, etc.)",
		},
	},
	Action: func(context *cli.Context) error {
		var (
			validate = context.String("validate")
			object   = context.Args().First()
		)
		ctx, cancel := appContext()
		defer cancel()

		if validate != "" {
			return errors.New("validating the edit result not supported")
		}

		// TODO(stevvooe): Support looking up objects by a reference through
		// the image metadata storage.

		dgst, err := digest.Parse(object)
		if err != nil {
			return err
		}

		conn, err := connectGRPC(context)
		if err != nil {
			return err
		}

		provider := contentservice.NewProviderFromClient(contentapi.NewContentClient(conn))
		ingester := contentservice.NewIngesterFromClient(contentapi.NewContentClient(conn))

		rc, err := provider.Reader(ctx, dgst)
		if err != nil {
			return err
		}
		defer rc.Close()

		nrc, err := edit(rc)
		if err != nil {
			return err
		}

		wr, err := ingester.Writer(ctx, "edit-"+object, 0, "") // TODO(stevvooe): Choose a better key?
		if err != nil {
			return err
		}

		if _, err := io.Copy(wr, nrc); err != nil {
			return err
		}

		if err := wr.Commit(0, wr.Digest()); err != nil {
			return err
		}

		fmt.Println(wr.Digest())
		return nil
	},
}

func edit(rd io.Reader) (io.ReadCloser, error) {
	tmp, err := ioutil.TempFile("", "edit-")
	if err != nil {
		return nil, err
	}

	if _, err := io.Copy(tmp, rd); err != nil {
		tmp.Close()
		return nil, err
	}

	cmd := exec.Command("sh", "-c", "$EDITOR "+tmp.Name())

	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = os.Environ()

	if err := cmd.Run(); err != nil {
		tmp.Close()
		return nil, err
	}

	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		tmp.Close()
		return nil, err
	}

	return tmp, nil
}
