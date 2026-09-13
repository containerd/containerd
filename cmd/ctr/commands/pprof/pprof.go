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

package pprof

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/containerd/containerd/v2/defaults"
	"github.com/urfave/cli/v3"
)

type pprofDialer struct {
	proto string
	addr  string
}

// Command is the cli command for providing golang pprof outputs for containerd
var Command = &cli.Command{
	Name:  "pprof",
	Usage: "Provide golang pprof outputs for containerd",
	Flags: []cli.Flag{
		&cli.StringFlag{
			Name:    "debug-socket",
			Aliases: []string{"d"},
			Usage:   "Socket path for containerd's debug server",
			Value:   defaults.DefaultDebugAddress,
		},
	},
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
	Usage: "Dump goroutine stack dump",
	Flags: []cli.Flag{
		&cli.UintFlag{
			Name:  "debug",
			Usage: "Debug pprof args",
			Value: 2,
		},
	},
	Action: func(ctx context.Context, cmd *cli.Command) error {
		return GoroutineProfile(cmd, getPProfClient)
	},
}

var pprofHeapCommand = &cli.Command{
	Name:  "heap",
	Usage: "Dump heap profile",
	Flags: []cli.Flag{
		&cli.UintFlag{
			Name:  "debug",
			Usage: "Debug pprof args",
			Value: 0,
		},
	},
	Action: func(ctx context.Context, cmd *cli.Command) error {
		return HeapProfile(cmd, getPProfClient)
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
			Usage: "Debug pprof args",
			Value: 0,
		},
	},
	Action: func(ctx context.Context, cmd *cli.Command) error {
		return CPUProfile(cmd, getPProfClient)
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
			Usage: "Debug pprof args",
			Value: 0,
		},
	},
	Action: func(ctx context.Context, cmd *cli.Command) error {
		return TraceProfile(cmd, getPProfClient)
	},
}

var pprofBlockCommand = &cli.Command{
	Name:  "block",
	Usage: "Goroutine blocking profile",
	Flags: []cli.Flag{
		&cli.UintFlag{
			Name:  "debug",
			Usage: "Debug pprof args",
			Value: 0,
		},
	},
	Action: func(ctx context.Context, cmd *cli.Command) error {
		return BlockProfile(cmd, getPProfClient)
	},
}

var pprofThreadcreateCommand = &cli.Command{
	Name:  "threadcreate",
	Usage: "Goroutine thread creating profile",
	Flags: []cli.Flag{
		&cli.UintFlag{
			Name:  "debug",
			Usage: "Debug pprof args",
			Value: 0,
		},
	},
	Action: func(ctx context.Context, cmd *cli.Command) error {
		return ThreadcreateProfile(cmd, getPProfClient)
	},
}

// Client is a func that returns a http client for a pprof server
type Client func(cmd *cli.Command) (*http.Client, error)

// GoroutineProfile dumps goroutine stack dump
func GoroutineProfile(cmd *cli.Command, clientFunc Client) error {
	client, err := clientFunc(cmd)
	if err != nil {
		return err
	}
	debug := cmd.Uint("debug")
	output, err := httpGetRequest(client, fmt.Sprintf("/debug/pprof/goroutine?debug=%d", debug))
	if err != nil {
		return err
	}
	defer output.Close()
	_, err = io.Copy(os.Stdout, output)
	return err
}

// HeapProfile dumps the heap profile
func HeapProfile(cmd *cli.Command, clientFunc Client) error {
	client, err := clientFunc(cmd)
	if err != nil {
		return err
	}
	debug := cmd.Uint("debug")
	output, err := httpGetRequest(client, fmt.Sprintf("/debug/pprof/heap?debug=%d", debug))
	if err != nil {
		return err
	}
	defer output.Close()
	_, err = io.Copy(os.Stdout, output)
	return err
}

// CPUProfile dumps CPU profile
func CPUProfile(cmd *cli.Command, clientFunc Client) error {
	client, err := clientFunc(cmd)
	if err != nil {
		return err
	}
	seconds := cmd.Duration("seconds").Seconds()
	debug := cmd.Uint("debug")
	output, err := httpGetRequest(client, fmt.Sprintf("/debug/pprof/profile?seconds=%v&debug=%d", seconds, debug))
	if err != nil {
		return err
	}
	defer output.Close()
	_, err = io.Copy(os.Stdout, output)
	return err
}

// TraceProfile collects execution trace
func TraceProfile(cmd *cli.Command, clientFunc Client) error {
	client, err := clientFunc(cmd)
	if err != nil {
		return err
	}
	seconds := cmd.Duration("seconds").Seconds()
	debug := cmd.Uint("debug")
	uri := fmt.Sprintf("/debug/pprof/trace?seconds=%v&debug=%d", seconds, debug)
	output, err := httpGetRequest(client, uri)
	if err != nil {
		return err
	}
	defer output.Close()
	_, err = io.Copy(os.Stdout, output)
	return err
}

// BlockProfile collects goroutine blocking profile
func BlockProfile(cmd *cli.Command, clientFunc Client) error {
	client, err := clientFunc(cmd)
	if err != nil {
		return err
	}
	debug := cmd.Uint("debug")
	output, err := httpGetRequest(client, fmt.Sprintf("/debug/pprof/block?debug=%d", debug))
	if err != nil {
		return err
	}
	defer output.Close()
	_, err = io.Copy(os.Stdout, output)
	return err
}

// ThreadcreateProfile collects goroutine thread creating profile
func ThreadcreateProfile(cmd *cli.Command, clientFunc Client) error {
	client, err := clientFunc(cmd)
	if err != nil {
		return err
	}
	debug := cmd.Uint("debug")
	output, err := httpGetRequest(client, fmt.Sprintf("/debug/pprof/threadcreate?debug=%d", debug))
	if err != nil {
		return err
	}
	defer output.Close()
	_, err = io.Copy(os.Stdout, output)
	return err
}

func getPProfClient(cmd *cli.Command) (*http.Client, error) {
	dialer := getPProfDialer(cmd.String("debug-socket"))

	tr := &http.Transport{
		DialContext: dialer.pprofDial,
	}
	client := &http.Client{Transport: tr}
	return client, nil
}

func httpGetRequest(client *http.Client, request string) (io.ReadCloser, error) {
	resp, err := client.Get("http://." + request)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("http get failed with status: %s", resp.Status)
	}
	return resp.Body, nil
}
