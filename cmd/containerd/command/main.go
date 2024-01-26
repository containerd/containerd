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

package command

import (
	gocontext "context"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"time"

	"github.com/containerd/containerd/v2/defaults"
	"github.com/containerd/containerd/v2/errdefs"
	_ "github.com/containerd/containerd/v2/metrics" // import containerd build info
	"github.com/containerd/containerd/v2/mount"
	"github.com/containerd/containerd/v2/services/server"
	srvconfig "github.com/containerd/containerd/v2/services/server/config"
	"github.com/containerd/containerd/v2/sys"
	"github.com/containerd/containerd/v2/version"
	"github.com/containerd/log"
	"github.com/urfave/cli"
	"google.golang.org/grpc/grpclog"
)

const usage = `
                    __        _                     __
  _________  ____  / /_____ _(_)___  ___  _________/ /
 / ___/ __ \/ __ \/ __/ __ ` + "`" + `/ / __ \/ _ \/ ___/ __  /
/ /__/ /_/ / / / / /_/ /_/ / / / / /  __/ /  / /_/ /
\___/\____/_/ /_/\__/\__,_/_/_/ /_/\___/_/   \__,_/

high performance container runtime
`

func init() {
	// Discard grpc logs so that they don't mess with our stdio
	grpclog.SetLoggerV2(grpclog.NewLoggerV2(io.Discard, io.Discard, io.Discard))

	cli.VersionPrinter = func(c *cli.Context) {
		fmt.Println(c.App.Name, version.Package, c.App.Version, version.Revision)
	}
	cli.VersionFlag = cli.BoolFlag{
		Name:  "version, v",
		Usage: "Print the version",
	}
	cli.HelpFlag = cli.BoolFlag{
		Name:  "help, h",
		Usage: "Show help",
	}
}

// App returns a *cli.App instance.
func App() *cli.App {
	app := cli.NewApp()
	app.Name = "containerd"
	app.Version = version.Version
	app.Usage = usage
	app.Description = `
containerd is a high performance container runtime whose daemon can be started
by using this command. If none of the *config*, *publish*, *oci-hook*, or *help* commands
are specified, the default action of the **containerd** command is to start the
containerd daemon in the foreground.


A default configuration is used if no TOML configuration is specified or located
at the default file location. The *containerd config* command can be used to
generate the default configuration for containerd. The output of that command
can be used and modified as necessary as a custom configuration.`
	app.Flags = []cli.Flag{
		cli.StringFlag{
			Name:  "config,c",
			Usage: "Path to the configuration file",
			Value: filepath.Join(defaults.DefaultConfigDir, "config.toml"),
		},
		cli.StringFlag{
			Name:  "log-level,l",
			Usage: "Set the logging level [trace, debug, info, warn, error, fatal, panic]",
		},
		cli.StringFlag{
			Name:  "address,a",
			Usage: "Address for containerd's GRPC server",
		},
		cli.StringFlag{
			Name:  "root",
			Usage: "containerd root directory",
		},
		cli.StringFlag{
			Name:  "state",
			Usage: "containerd state directory",
		},
	}
	app.Flags = append(app.Flags, serviceFlags()...)
	app.Commands = []cli.Command{
		configCommand,
		publishCommand,
		ociHook,
	}
	app.Action = func(context *cli.Context) error {
		var (
			start       = time.Now()
			signals     = make(chan os.Signal, 2048)
			serverC     = make(chan *server.Server, 1)
			ctx, cancel = gocontext.WithCancel(gocontext.Background())
			config      = defaultConfig()
		)

		defer cancel()

		// Only try to load the config if it either exists, or the user explicitly
		// told us to load this path.
		configPath := context.GlobalString("config")
		_, err := os.Stat(configPath)
		if !os.IsNotExist(err) || context.GlobalIsSet("config") {
			if err := srvconfig.LoadConfig(ctx, configPath, config); err != nil {
				return err
			}
		}

		// Apply flags to the config
		if err := applyFlags(context, config); err != nil {
			return err
		}

		if config.GRPC.Address == "" {
			return fmt.Errorf("grpc address cannot be empty: %w", errdefs.ErrInvalidArgument)
		}
		if config.TTRPC.Address == "" {
			// If TTRPC was not explicitly configured, use defaults based on GRPC.
			config.TTRPC.Address = config.GRPC.Address + ".ttrpc"
			config.TTRPC.UID = config.GRPC.UID
			config.TTRPC.GID = config.GRPC.GID
		}

		// Make sure top-level directories are created early.
		if err := server.CreateTopLevelDirectories(config); err != nil {
			return err
		}

		// Stop if we are registering or unregistering against Windows SCM.
		stop, err := registerUnregisterService(config.Root)
		if err != nil {
			log.L.Fatal(err)
		}
		if stop {
			return nil
		}

		done := handleSignals(ctx, signals, serverC, cancel)
		// start the signal handler as soon as we can to make sure that
		// we don't miss any signals during boot
		signal.Notify(signals, handledSignals...)

		// cleanup temp mounts
		if err := mount.SetTempMountLocation(filepath.Join(config.Root, "tmpmounts")); err != nil {
			return fmt.Errorf("creating temp mount location: %w", err)
		}
		// unmount all temp mounts on boot for the server
		warnings, err := mount.CleanupTempMounts(0)
		if err != nil {
			log.G(ctx).WithError(err).Error("unmounting temp mounts")
		}
		for _, w := range warnings {
			log.G(ctx).WithError(w).Warn("cleanup temp mount")
		}

		log.G(ctx).WithFields(log.Fields{
			"version":  version.Version,
			"revision": version.Revision,
		}).Info("starting containerd")

		type srvResp struct {
			s   *server.Server
			err error
		}

		// run server initialization in a goroutine so we don't end up blocking important things like SIGTERM handling
		// while the server is initializing.
		// As an example, opening the bolt database blocks forever if a containerd instance
		// is already running, which must then be forcibly terminated (SIGKILL) to recover.
		chsrv := make(chan srvResp)
		go func() {
			defer close(chsrv)

			server, err := server.New(ctx, config)
			if err != nil {
				select {
				case chsrv <- srvResp{err: err}:
				case <-ctx.Done():
				}
				return
			}

			// Launch as a Windows Service if necessary
			if err := launchService(server, done); err != nil {
				log.L.Fatal(err)
			}
			select {
			case <-ctx.Done():
				server.Stop()
			case chsrv <- srvResp{s: server}:
			}
		}()

		var server *server.Server
		select {
		case <-ctx.Done():
			return ctx.Err()
		case r := <-chsrv:
			if r.err != nil {
				return r.err
			}
			server = r.s
		}

		// We don't send the server down serverC directly in the goroutine above because we need it lower down.
		select {
		case <-ctx.Done():
			return ctx.Err()
		case serverC <- server:
		}

		if config.Debug.Address != "" {
			var l net.Listener
			if isLocalAddress(config.Debug.Address) {
				if l, err = sys.GetLocalListener(config.Debug.Address, config.Debug.UID, config.Debug.GID); err != nil {
					return fmt.Errorf("failed to get listener for debug endpoint: %w", err)
				}
			} else {
				if l, err = net.Listen("tcp", config.Debug.Address); err != nil {
					return fmt.Errorf("failed to get listener for debug endpoint: %w", err)
				}
			}
			serve(ctx, l, server.ServeDebug)
		}
		if config.Metrics.Address != "" {
			l, err := net.Listen("tcp", config.Metrics.Address)
			if err != nil {
				return fmt.Errorf("failed to get listener for metrics endpoint: %w", err)
			}
			serve(ctx, l, server.ServeMetrics)
		}
		// setup the ttrpc endpoint
		tl, err := sys.GetLocalListener(config.TTRPC.Address, config.TTRPC.UID, config.TTRPC.GID)
		if err != nil {
			return fmt.Errorf("failed to get listener for main ttrpc endpoint: %w", err)
		}
		serve(ctx, tl, server.ServeTTRPC)

		if config.GRPC.TCPAddress != "" {
			l, err := net.Listen("tcp", config.GRPC.TCPAddress)
			if err != nil {
				return fmt.Errorf("failed to get listener for TCP grpc endpoint: %w", err)
			}
			serve(ctx, l, server.ServeTCP)
		}
		// setup the main grpc endpoint
		l, err := sys.GetLocalListener(config.GRPC.Address, config.GRPC.UID, config.GRPC.GID)
		if err != nil {
			return fmt.Errorf("failed to get listener for main endpoint: %w", err)
		}
		serve(ctx, l, server.ServeGRPC)

		readyC := make(chan struct{})
		go func() {
			server.Wait()
			close(readyC)
		}()

		select {
		case <-readyC:
			if err := notifyReady(ctx); err != nil {
				log.G(ctx).WithError(err).Warn("notify ready failed")
			}
			log.G(ctx).Infof("containerd successfully booted in %fs", time.Since(start).Seconds())
			<-done
		case <-done:
		}
		return nil
	}
	return app
}

func serve(ctx gocontext.Context, l net.Listener, serveFunc func(net.Listener) error) {
	path := l.Addr().String()
	log.G(ctx).WithField("address", path).Info("serving...")
	go func() {
		defer l.Close()
		if err := serveFunc(l); err != nil {
			log.G(ctx).WithError(err).WithField("address", path).Fatal("serve failure")
		}
	}()
}

func applyFlags(context *cli.Context, config *srvconfig.Config) error {
	// the order for config vs flag values is that flags will always override
	// the config values if they are set
	if err := setLogLevel(context, config); err != nil {
		return err
	}
	if err := setLogFormat(config); err != nil {
		return err
	}

	for _, v := range []struct {
		name string
		d    *string
	}{
		{
			name: "root",
			d:    &config.Root,
		},
		{
			name: "state",
			d:    &config.State,
		},
		{
			name: "address",
			d:    &config.GRPC.Address,
		},
	} {
		if s := context.GlobalString(v.name); s != "" {
			*v.d = s
			if v.name == "root" || v.name == "state" {
				absPath, err := filepath.Abs(s)
				if err != nil {
					return err
				}
				*v.d = absPath
			}
		}
	}

	applyPlatformFlags(context)

	return nil
}

func setLogLevel(context *cli.Context, config *srvconfig.Config) error {
	l := context.GlobalString("log-level")
	if l == "" {
		l = config.Debug.Level
	}
	if l != "" {
		return log.SetLevel(l)
	}
	return nil
}

func setLogFormat(config *srvconfig.Config) error {
	f := log.OutputFormat(config.Debug.Format)
	if f == "" {
		f = log.TextFormat
	}

	return log.SetFormat(f)
}

func dumpStacks(writeToFile bool) {
	var (
		buf       []byte
		stackSize int
	)
	bufferLen := 16384
	for stackSize == len(buf) {
		buf = make([]byte, bufferLen)
		stackSize = runtime.Stack(buf, true)
		bufferLen *= 2
	}
	buf = buf[:stackSize]
	log.L.Infof("=== BEGIN goroutine stack dump ===\n%s\n=== END goroutine stack dump ===", buf)

	if writeToFile {
		// Also write to file to aid gathering diagnostics
		name := filepath.Join(os.TempDir(), fmt.Sprintf("containerd.%d.stacks.log", os.Getpid()))
		f, err := os.Create(name)
		if err != nil {
			return
		}
		defer f.Close()
		f.WriteString(string(buf))
		log.L.Infof("goroutine stack dump written to %s", name)
	}
}
