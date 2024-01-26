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
	"github.com/urfave/cli"
)

type pprofDialer struct {
	proto string
	addr  string
}

// Command is the cli command for providing golang pprof outputs for containerd
var Command = cli.Command{
	Name:  "pprof",
	Usage: "Provide golang pprof outputs for containerd",
	Flags: []cli.Flag{
		cli.StringFlag{
			Name:  "debug-socket, d",
			Usage: "Socket path for containerd's debug server",
			Value: defaults.DefaultDebugAddress,
		},
	},
	Subcommands: []cli.Command{
		pprofBlockCommand,
		pprofGoroutinesCommand,
		pprofHeapCommand,
		pprofProfileCommand,
		pprofThreadcreateCommand,
		pprofTraceCommand,
	},
}

var pprofGoroutinesCommand = cli.Command{
	Name:  "goroutines",
	Usage: "Dump goroutine stack dump",
	Flags: []cli.Flag{
		cli.UintFlag{
			Name:  "debug",
			Usage: "Debug pprof args",
			Value: 2,
		},
	},
	Action: func(context *cli.Context) error {
		client := getPProfClient(context)

		debug := context.Uint("debug")
		output, err := httpGetRequest(client, fmt.Sprintf("/debug/pprof/goroutine?debug=%d", debug))
		if err != nil {
			return err
		}
		defer output.Close()
		_, err = io.Copy(os.Stdout, output)
		return err
	},
}

var pprofHeapCommand = cli.Command{
	Name:  "heap",
	Usage: "Dump heap profile",
	Flags: []cli.Flag{
		cli.UintFlag{
			Name:  "debug",
			Usage: "Debug pprof args",
			Value: 0,
		},
	},
	Action: func(context *cli.Context) error {
		client := getPProfClient(context)

		debug := context.Uint("debug")
		output, err := httpGetRequest(client, fmt.Sprintf("/debug/pprof/heap?debug=%d", debug))
		if err != nil {
			return err
		}
		defer output.Close()
		_, err = io.Copy(os.Stdout, output)
		return err
	},
}

var pprofProfileCommand = cli.Command{
	Name:  "profile",
	Usage: "CPU profile",
	Flags: []cli.Flag{
		cli.DurationFlag{
			Name:  "seconds,s",
			Usage: "Duration for collection (seconds)",
			Value: 30 * time.Second,
		},
		cli.UintFlag{
			Name:  "debug",
			Usage: "Debug pprof args",
			Value: 0,
		},
	},
	Action: func(context *cli.Context) error {
		client := getPProfClient(context)

		seconds := context.Duration("seconds").Seconds()
		debug := context.Uint("debug")
		output, err := httpGetRequest(client, fmt.Sprintf("/debug/pprof/profile?seconds=%v&debug=%d", seconds, debug))
		if err != nil {
			return err
		}
		defer output.Close()
		_, err = io.Copy(os.Stdout, output)
		return err
	},
}

var pprofTraceCommand = cli.Command{
	Name:  "trace",
	Usage: "Collect execution trace",
	Flags: []cli.Flag{
		cli.DurationFlag{
			Name:  "seconds,s",
			Usage: "Trace time (seconds)",
			Value: 5 * time.Second,
		},
		cli.UintFlag{
			Name:  "debug",
			Usage: "Debug pprof args",
			Value: 0,
		},
	},
	Action: func(context *cli.Context) error {
		client := getPProfClient(context)

		seconds := context.Duration("seconds").Seconds()
		debug := context.Uint("debug")
		uri := fmt.Sprintf("/debug/pprof/trace?seconds=%v&debug=%d", seconds, debug)
		output, err := httpGetRequest(client, uri)
		if err != nil {
			return err
		}
		defer output.Close()
		_, err = io.Copy(os.Stdout, output)
		return err
	},
}

var pprofBlockCommand = cli.Command{
	Name:  "block",
	Usage: "Goroutine blocking profile",
	Flags: []cli.Flag{
		cli.UintFlag{
			Name:  "debug",
			Usage: "Debug pprof args",
			Value: 0,
		},
	},
	Action: func(context *cli.Context) error {
		client := getPProfClient(context)

		debug := context.Uint("debug")
		output, err := httpGetRequest(client, fmt.Sprintf("/debug/pprof/block?debug=%d", debug))
		if err != nil {
			return err
		}
		defer output.Close()
		_, err = io.Copy(os.Stdout, output)
		return err
	},
}

var pprofThreadcreateCommand = cli.Command{
	Name:  "threadcreate",
	Usage: "Goroutine thread creating profile",
	Flags: []cli.Flag{
		cli.UintFlag{
			Name:  "debug",
			Usage: "Debug pprof args",
			Value: 0,
		},
	},
	Action: func(context *cli.Context) error {
		client := getPProfClient(context)

		debug := context.Uint("debug")
		output, err := httpGetRequest(client, fmt.Sprintf("/debug/pprof/threadcreate?debug=%d", debug))
		if err != nil {
			return err
		}
		defer output.Close()
		_, err = io.Copy(os.Stdout, output)
		return err
	},
}

func getPProfClient(context *cli.Context) *http.Client {
	dialer := getPProfDialer(context.GlobalString("debug-socket"))

	tr := &http.Transport{
		Dial: dialer.pprofDial,
	}
	client := &http.Client{Transport: tr}
	return client
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
