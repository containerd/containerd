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
	"github.com/urfave/cli/v3"
)

var pprofCommand = &cli.Command{
	Name:  "pprof",
	Usage: "Provide golang pprof outputs for containerd-shim",
	Commands: []*cli.Command{
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
	Action: func(ctx context.Context, cmd *cli.Command) error {
		return pprof.GoroutineProfile(cmd, getPProfClient)
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
	Action: func(ctx context.Context, cmd *cli.Command) error {
		return pprof.HeapProfile(cmd, getPProfClient)
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
	Action: func(ctx context.Context, cmd *cli.Command) error {
		return pprof.CPUProfile(cmd, getPProfClient)
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
	Action: func(ctx context.Context, cmd *cli.Command) error {
		return pprof.TraceProfile(cmd, getPProfClient)
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
	Action: func(ctx context.Context, cmd *cli.Command) error {
		return pprof.BlockProfile(cmd, getPProfClient)
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
	Action: func(ctx context.Context, cmd *cli.Command) error {
		return pprof.ThreadcreateProfile(cmd, getPProfClient)
	},
}

func getPProfClient(cmd *cli.Command) (*http.Client, error) {
	id := cmd.String("id")
	if id == "" {
		return nil, errors.New("container id must be provided")
	}
	tr := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			ns := cmd.String("namespace")
			ctx = namespaces.WithNamespace(ctx, ns)
			s, _ := shim.SocketAddress(ctx, cmd.String("address"), id, true)
			s = strings.TrimPrefix(s, "unix://")
			var dialer net.Dialer
			return dialer.DialContext(ctx, "unix", s)
		},
	}
	return &http.Client{Transport: tr}, nil
}
