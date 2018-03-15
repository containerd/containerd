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

package run

import (
	gocontext "context"

	"github.com/containerd/console"
	"github.com/containerd/containerd"
	"github.com/containerd/containerd/cmd/ctr/commands"
	"github.com/containerd/containerd/containers"
	"github.com/containerd/containerd/oci"
	specs "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/sirupsen/logrus"
	"github.com/urfave/cli"
)

func withTTY(terminal bool) oci.SpecOpts {
	if !terminal {
		return func(ctx gocontext.Context, client oci.Client, c *containers.Container, s *specs.Spec) error {
			s.Process.Terminal = false
			return nil
		}
	}

	con := console.Current()
	size, err := con.Size()
	if err != nil {
		logrus.WithError(err).Error("console size")
	}
	return oci.WithTTY(int(size.Width), int(size.Height))
}

// NewContainer creates a new container
func NewContainer(ctx gocontext.Context, client *containerd.Client, context *cli.Context) (containerd.Container, error) {
	var (
		ref  = context.Args().First()
		id   = context.Args().Get(1)
		args = context.Args()[2:]
	)

	image, err := client.GetImage(ctx, ref)
	if err != nil {
		return nil, err
	}

	var (
		opts  []oci.SpecOpts
		cOpts []containerd.NewContainerOpts
		spec  containerd.NewContainerOpts
	)
	opts = append(opts, oci.WithImageConfig(image))
	opts = append(opts, oci.WithEnv(context.StringSlice("env")))
	opts = append(opts, withMounts(context))
	opts = append(opts, withTTY(context.Bool("tty")))
	if len(args) > 0 {
		opts = append(opts, oci.WithProcessArgs(args...))
	}
	if cwd := context.String("cwd"); cwd != "" {
		opts = append(opts, oci.WithProcessCwd(cwd))
	}

	if context.IsSet("config") {
		var s specs.Spec
		if err := loadSpec(context.String("config"), &s); err != nil {
			return nil, err
		}
		spec = containerd.WithSpec(&s, opts...)
	} else {
		spec = containerd.WithNewSpec(opts...)
	}

	cOpts = append(cOpts, containerd.WithContainerLabels(commands.LabelArgs(context.StringSlice("label"))))
	cOpts = append(cOpts, containerd.WithImage(image))
	cOpts = append(cOpts, containerd.WithSnapshotter(context.String("snapshotter")))
	cOpts = append(cOpts, containerd.WithNewSnapshot(id, image))
	cOpts = append(cOpts, containerd.WithRuntime(context.String("runtime"), nil))
	cOpts = append(cOpts, spec)

	return client.NewContainer(ctx, id, cOpts...)
}

func getNewTaskOpts(_ *cli.Context) []containerd.NewTaskOpts {
	return nil
}
