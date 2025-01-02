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
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/containerd/containerd/v2/defaults"
	"github.com/urfave/cli/v2"
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
	Usage: "Dump goroutine stack dump",
	Flags: []cli.Flag{
		&cli.UintFlag{
			Name:  "debug",
			Usage: "Debug pprof args",
			Value: 2,
		},
	},
	Action: func(cliContext *cli.Context) error {
		return GoroutineProfile(cliContext, getPProfClient)
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
	Action: func(cliContext *cli.Context) error {
		return HeapProfile(cliContext, getPProfClient)
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
	Action: func(cliContext *cli.Context) error {
		return CPUProfile(cliContext, getPProfClient)
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
	Action: func(cliContext *cli.Context) error {
		return TraceProfile(cliContext, getPProfClient)
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
	Action: func(cliContext *cli.Context) error {
		return BlockProfile(cliContext, getPProfClient)
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
	Action: func(cliContext *cli.Context) error {
		return ThreadcreateProfile(cliContext, getPProfClient)
	},
}

// Client is a func that returns a http client for a pprof server
type Client func(cliContext *cli.Context) (*http.Client, error)

// GoroutineProfile dumps goroutine stack dump
func GoroutineProfile(cliContext *cli.Context, clientFunc Client) error {
	client, err := clientFunc(cliContext)
	if err != nil {
		return err
	}
	debug := cliContext.Uint("debug")
	output, err := httpGetRequest(client, fmt.Sprintf("/debug/pprof/goroutine?debug=%d", debug))
	if err != nil {
		return err
	}
	defer output.Close()
	_, err = io.Copy(os.Stdout, output)
	return err
}

// HeapProfile dumps the heap profile
func HeapProfile(cliContext *cli.Context, clientFunc Client) error {
	client, err := clientFunc(cliContext)
	if err != nil {
		return err
	}
	debug := cliContext.Uint("debug")
	output, err := httpGetRequest(client, fmt.Sprintf("/debug/pprof/heap?debug=%d", debug))
	if err != nil {
		return err
	}
	defer output.Close()
	_, err = io.Copy(os.Stdout, output)
	return err
}

// CPUProfile dumps CPU profile
func CPUProfile(cliContext *cli.Context, clientFunc Client) error {
	client, err := clientFunc(cliContext)
	if err != nil {
		return err
	}
	seconds := cliContext.Duration("seconds").Seconds()
	debug := cliContext.Uint("debug")
	output, err := httpGetRequest(client, fmt.Sprintf("/debug/pprof/profile?seconds=%v&debug=%d", seconds, debug))
	if err != nil {
		return err
	}
	defer output.Close()
	_, err = io.Copy(os.Stdout, output)
	return err
}

// TraceProfile collects execution trace
func TraceProfile(cliContext *cli.Context, clientFunc Client) error {
	client, err := clientFunc(cliContext)
	if err != nil {
		return err
	}
	seconds := cliContext.Duration("seconds").Seconds()
	debug := cliContext.Uint("debug")
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
func BlockProfile(cliContext *cli.Context, clientFunc Client) error {
	client, err := clientFunc(cliContext)
	if err != nil {
		return err
	}
	debug := cliContext.Uint("debug")
	output, err := httpGetRequest(client, fmt.Sprintf("/debug/pprof/block?debug=%d", debug))
	if err != nil {
		return err
	}
	defer output.Close()
	_, err = io.Copy(os.Stdout, output)
	return err
}

// ThreadcreateProfile collects goroutine thread creating profile
func ThreadcreateProfile(cliContext *cli.Context, clientFunc Client) error {
	client, err := clientFunc(cliContext)
	if err != nil {
		return err
	}
	debug := cliContext.Uint("debug")
	output, err := httpGetRequest(client, fmt.Sprintf("/debug/pprof/threadcreate?debug=%d", debug))
	if err != nil {
		return err
	}
	defer output.Close()
	_, err = io.Copy(os.Stdout, output)
	return err
}

func getPProfClient(cliContext *cli.Context) (*http.Client, error) {
	dialer := getPProfDialer(cliContext.String("debug-socket"))

	tr := &http.Transport{
		Dial: dialer.pprofDial,
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
