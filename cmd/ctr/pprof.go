package main

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/urfave/cli"
)

type pprofDialer struct {
	proto string
	addr  string
}

var pprofCommand = cli.Command{
	Name:  "pprof",
	Usage: "provides golang pprof outputs for containerd",
	Flags: []cli.Flag{
		cli.StringFlag{
			Name:  "debug-socket, d",
			Usage: "socket path for containerd's debug server",
			Value: "/run/containerd/containerd-debug.sock",
		},
	},
	Subcommands: []cli.Command{
		pprofGoroutinesCommand,
		pprofHeapCommand,
		pprofProfileCommand,
		pprofTraceCommand,
		pprofBlockCommand,
		pprofThreadcreateCommand,
	},
}

var pprofGoroutinesCommand = cli.Command{
	Name:  "goroutines",
	Usage: "dump goroutine stack dump",
	Action: func(context *cli.Context) error {
		client := getPProfClient(context)

		output, err := httpGetRequest(client, "/debug/pprof/goroutine?debug=2")
		if err != nil {
			return err
		}
		defer output.Close()
		if _, err := io.Copy(os.Stdout, output); err != nil {
			return err
		}
		return nil
	},
}

var pprofHeapCommand = cli.Command{
	Name:  "heap",
	Usage: "dump heap profile",
	Action: func(context *cli.Context) error {
		client := getPProfClient(context)

		output, err := httpGetRequest(client, "/debug/pprof/heap")
		if err != nil {
			return err
		}
		defer output.Close()
		if _, err := io.Copy(os.Stdout, output); err != nil {
			return err
		}
		return nil
	},
}

var pprofProfileCommand = cli.Command{
	Name:  "profile",
	Usage: "CPU profile",
	Action: func(context *cli.Context) error {
		client := getPProfClient(context)

		output, err := httpGetRequest(client, "/debug/pprof/profile")
		if err != nil {
			return err
		}
		defer output.Close()
		if _, err := io.Copy(os.Stdout, output); err != nil {
			return err
		}
		return nil
	},
}

var pprofTraceCommand = cli.Command{
	Name:  "trace",
	Usage: "collects execution trace",
	Flags: []cli.Flag{
		cli.DurationFlag{
			Name:  "seconds,s",
			Usage: "Trace time (seconds)",
			Value: time.Duration(5 * time.Second),
		},
	},
	Action: func(context *cli.Context) error {
		client := getPProfClient(context)

		seconds := context.Duration("seconds").Seconds()
		uri := fmt.Sprintf("/debug/pprof/trace?seconds=%v", seconds)
		output, err := httpGetRequest(client, uri)
		if err != nil {
			return err
		}
		defer output.Close()
		if _, err := io.Copy(os.Stdout, output); err != nil {
			return err
		}
		return nil

	},
}

var pprofBlockCommand = cli.Command{
	Name:  "block",
	Usage: "goroutine blocking profile",
	Action: func(context *cli.Context) error {
		client := getPProfClient(context)

		output, err := httpGetRequest(client, "/debug/pprof/block")
		if err != nil {
			return err
		}
		defer output.Close()
		if _, err := io.Copy(os.Stdout, output); err != nil {
			return err
		}
		return nil
	},
}

var pprofThreadcreateCommand = cli.Command{
	Name:  "threadcreate",
	Usage: "goroutine blocking profile",
	Action: func(context *cli.Context) error {
		client := getPProfClient(context)

		output, err := httpGetRequest(client, "/debug/pprof/threadcreate")
		if err != nil {
			return err
		}
		defer output.Close()
		if _, err := io.Copy(os.Stdout, output); err != nil {
			return err
		}
		return nil
	},
}

func (d *pprofDialer) pprofDial(proto, addr string) (conn net.Conn, err error) {
	return net.Dial(d.proto, d.addr)
}

func getPProfClient(context *cli.Context) *http.Client {
	addr := context.GlobalString("debug-socket")
	dialer := pprofDialer{"unix", addr}

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
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("%s", resp.Status)
	}
	return resp.Body, nil
}
