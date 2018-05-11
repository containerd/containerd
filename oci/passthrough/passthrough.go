// +build linux

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

package passthrough

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/containerd/containerd/containers"
	"github.com/containerd/containerd/namespaces"
	"github.com/containerd/containerd/oci"
	specs "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/urfave/cli"
)

// DefaultDirectory for spec passthrough plugins to be installed
const DefaultDirectory = "/opt/containerd/bin"

// Opts specifies command specific options for executing the passthrough plugins
type Opts func(*exec.Cmd) error

// Main executes the cli harness for running spec passthough plugins
func Main(name string, opts ...oci.SpecOpts) {
	app := cli.NewApp()
	app.Name = name
	app.Flags = []cli.Flag{
		cli.StringFlag{
			Name:  "namespace,ns",
			Value: "default",
			Usage: "namespace for the container or spec",
		},
		cli.StringFlag{
			Name:  "id",
			Usage: "container id",
		},
	}
	app.Action = func(cliCtx *cli.Context) error {
		ctx := namespaces.WithNamespace(context.Background(), cliCtx.GlobalString("namespace"))
		s, err := readSpec(os.Stdin)
		if err != nil {
			return err
		}
		container := &containers.Container{
			ID: cliCtx.GlobalString("id"),
		}
		for _, o := range opts {
			if err := o(ctx, nil, container, s); err != nil {
				return err
			}
		}
		return json.NewEncoder(os.Stdout).Encode(s)
	}
	if err := app.Run(os.Args); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
}

func readSpec(r io.Reader) (*specs.Spec, error) {
	var s specs.Spec
	if err := json.NewDecoder(r).Decode(&s); err != nil {
		return nil, err
	}
	return &s, nil
}

// WithSpecPassthrough allows binaries to modify the spec of the container
func WithSpecPassthrough(opts ...Opts) oci.SpecOpts {
	return WithSpecPassthroughDir(DefaultDirectory, opts...)
}

// WithSpecPassthroughDir allows binaries in the specified path to modify the spec of the container
func WithSpecPassthroughDir(dir string, opts ...Opts) oci.SpecOpts {
	return func(ctx context.Context, _ oci.Client, c *containers.Container, s *specs.Spec) error {
		binaries, err := ioutil.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		ns := *s
		for _, binary := range binaries {
			r, err := execute(ctx, c.ID, filepath.Join(dir, binary.Name()), &ns, opts)
			if err != nil {
				return err
			}
			// reset spec for next modification
			ns = *r
		}
		(*s) = ns
		return nil
	}
}

func execute(ctx context.Context, id, name string, s *specs.Spec, opts []Opts) (*specs.Spec, error) {
	ns, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return nil, err
	}
	// run command as unprivileged or let user provide configuration
	cmd := exec.CommandContext(ctx, name, "--namespace", ns, "--id", id)
	for _, o := range opts {
		if err := o(cmd); err != nil {
			return nil, err
		}
	}
	r, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	defer r.Close()
	w, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	if err := json.NewEncoder(w).Encode(s); err != nil {
		cmd.Process.Kill()
		cmd.Wait()
		return nil, err
	}
	w.Close()
	var newSpec specs.Spec
	if err := json.NewDecoder(r).Decode(&newSpec); err != nil {
		cmd.Process.Kill()
		cmd.Wait()
		return nil, err
	}
	if err := cmd.Wait(); err != nil {
		return nil, err
	}
	return &newSpec, nil
}
