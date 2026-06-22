//go:build !windows

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

package shim

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/containerd/containerd/v2/cmd/ctr/commands/pprof"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/containerd/containerd/v2/pkg/shim"
	"github.com/urfave/cli/v2"
)

var pprofCommand = &cli.Command{
	Name:  "pprof",
	Usage: "Provide golang pprof outputs for containerd-shim",
	Subcommands: []*cli.Command{
		pprofBlockCommand,
		pprofGoroutinesCommand,
		pprofHeapCommand,
		pprofProfileCommand,
		pprofThreadcreateCommand,
		pprofTraceCommand,
	},
}

var pprofGoroutinesCommand = &cli.Command{
	Name:  "goroutines",
	Usage: "Print goroutine stack dump",
	Flags: []cli.Flag{
		&cli.UintFlag{
			Name:  "debug",
			Usage: "Output format, value = 0: binary, value > 0: plaintext",
			Value: 2,
		},
	},
	Action: func(cliContext *cli.Context) error {
		return pprof.GoroutineProfile(cliContext, getPProfClient)
	},
}

var pprofHeapCommand = &cli.Command{
	Name:  "heap",
	Usage: "Dump heap profile",
	Flags: []cli.Flag{
		&cli.UintFlag{
			Name:  "debug",
			Usage: "Output format, value = 0: binary, value > 0: plaintext",
			Value: 0,
		},
	},
	Action: func(cliContext *cli.Context) error {
		return pprof.HeapProfile(cliContext, getPProfClient)
	},
}

var pprofProfileCommand = &cli.Command{
	Name:  "profile",
	Usage: "CPU profile",
	Flags: []cli.Flag{
		&cli.DurationFlag{
			Name:    "seconds",
			Aliases: []string{"s"},
			Usage:   "Duration for collection (seconds)",
			Value:   30 * time.Second,
		},
		&cli.UintFlag{
			Name:  "debug",
			Usage: "Output format, value = 0: binary, value > 0: plaintext",
			Value: 0,
		},
	},
	Action: func(cliContext *cli.Context) error {
		return pprof.CPUProfile(cliContext, getPProfClient)
	},
}

var pprofTraceCommand = &cli.Command{
	Name:  "trace",
	Usage: "Collect execution trace",
	Flags: []cli.Flag{
		&cli.DurationFlag{
			Name:    "seconds",
			Aliases: []string{"s"},
			Usage:   "Trace time (seconds)",
			Value:   5 * time.Second,
		},
		&cli.UintFlag{
			Name:  "debug",
			Usage: "Output format, value = 0: binary, value > 0: plaintext",
			Value: 0,
		},
	},
	Action: func(cliContext *cli.Context) error {
		return pprof.TraceProfile(cliContext, getPProfClient)
	},
}

var pprofBlockCommand = &cli.Command{
	Name:  "block",
	Usage: "Goroutine blocking profile",
	Flags: []cli.Flag{
		&cli.UintFlag{
			Name:  "debug",
			Usage: "Output format, value = 0: binary, value > 0: plaintext",
			Value: 0,
		},
	},
	Action: func(cliContext *cli.Context) error {
		return pprof.BlockProfile(cliContext, getPProfClient)
	},
}

var pprofThreadcreateCommand = &cli.Command{
	Name:  "threadcreate",
	Usage: "Goroutine thread creating profile",
	Flags: []cli.Flag{
		&cli.UintFlag{
			Name:  "debug",
			Usage: "Output format, value = 0: binary, value > 0: plaintext",
			Value: 0,
		},
	},
	Action: func(cliContext *cli.Context) error {
		return pprof.ThreadcreateProfile(cliContext, getPProfClient)
	},
}

func getPProfClient(cliContext *cli.Context) (*http.Client, error) {
	id := cliContext.String("id")
	if id == "" {
		return nil, errors.New("container id must be provided")
	}
	tr := &http.Transport{
		Dial: func(_, _ string) (net.Conn, error) {
			ns := cliContext.String("namespace")
			ctx := namespaces.WithNamespace(context.Background(), ns)
			s, _ := shim.SocketAddress(ctx, cliContext.String("address"), id, true)
			s = strings.TrimPrefix(s, "unix://")
			return net.Dial("unix", s)
		},
	}
	return &http.Client{Transport: tr}, nil
}
