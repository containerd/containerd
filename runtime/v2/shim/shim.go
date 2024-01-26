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
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"time"

	shimapi "github.com/containerd/containerd/v2/api/runtime/task/v3"
	"github.com/containerd/containerd/v2/events"
	"github.com/containerd/containerd/v2/namespaces"
	"github.com/containerd/containerd/v2/pkg/shutdown"
	"github.com/containerd/containerd/v2/plugins"
	"github.com/containerd/containerd/v2/protobuf"
	"github.com/containerd/containerd/v2/protobuf/proto"
	"github.com/containerd/containerd/v2/version"
	"github.com/containerd/log"
	"github.com/containerd/plugin"
	"github.com/containerd/plugin/registry"
	"github.com/containerd/ttrpc"
	"github.com/sirupsen/logrus"
)

// Publisher for events
type Publisher interface {
	events.Publisher
	io.Closer
}

// StartOpts describes shim start configuration received from containerd
type StartOpts struct {
	Address      string
	TTRPCAddress string
	Debug        bool
}

// BootstrapParams is a JSON payload returned in stdout from shim.Start call.
type BootstrapParams struct {
	// Version is the version of shim parameters (expected 2 for shim v2)
	Version int `json:"version"`
	// Address is a address containerd should use to connect to shim.
	Address string `json:"address"`
	// Protocol is either TTRPC or GRPC.
	Protocol string `json:"protocol"`
}

type StopStatus struct {
	Pid        int
	ExitStatus int
	ExitedAt   time.Time
}

// Manager is the interface which manages the shim process
type Manager interface {
	Name() string
	Start(ctx context.Context, id string, opts StartOpts) (BootstrapParams, error)
	Stop(ctx context.Context, id string) (StopStatus, error)
}

// OptsKey is the context key for the Opts value.
type OptsKey struct{}

// Opts are context options associated with the shim invocation.
type Opts struct {
	BundlePath string
	Debug      bool
}

// BinaryOpts allows the configuration of a shims binary setup
type BinaryOpts func(*Config)

// Config of shim binary options provided by shim implementations
type Config struct {
	// NoSubreaper disables setting the shim as a child subreaper
	NoSubreaper bool
	// NoReaper disables the shim binary from reaping any child process implicitly
	NoReaper bool
	// NoSetupLogger disables automatic configuration of logrus to use the shim FIFO
	NoSetupLogger bool
}

type TTRPCService interface {
	RegisterTTRPC(*ttrpc.Server) error
}

type TTRPCServerOptioner interface {
	TTRPCService

	UnaryInterceptor() ttrpc.UnaryServerInterceptor
}

var (
	debugFlag            bool
	versionFlag          bool
	id                   string
	namespaceFlag        string
	socketFlag           string
	bundlePath           string
	addressFlag          string
	containerdBinaryFlag string
	action               string
)

const (
	ttrpcAddressEnv = "TTRPC_ADDRESS"
	grpcAddressEnv  = "GRPC_ADDRESS"
	namespaceEnv    = "NAMESPACE"
	maxVersionEnv   = "MAX_SHIM_VERSION"
)

func parseFlags() {
	flag.BoolVar(&debugFlag, "debug", false, "enable debug output in logs")
	flag.BoolVar(&versionFlag, "v", false, "show the shim version and exit")
	flag.StringVar(&namespaceFlag, "namespace", "", "namespace that owns the shim")
	flag.StringVar(&id, "id", "", "id of the task")
	flag.StringVar(&socketFlag, "socket", "", "socket path to serve")
	flag.StringVar(&bundlePath, "bundle", "", "path to the bundle if not workdir")

	flag.StringVar(&addressFlag, "address", "", "grpc address back to main containerd")
	flag.StringVar(&containerdBinaryFlag, "publish-binary", "",
		fmt.Sprintf("path to publish binary (used for publishing events), but %s will ignore this flag, please use the %s env", os.Args[0], ttrpcAddressEnv),
	)

	flag.Parse()
	action = flag.Arg(0)
}

func setRuntime() {
	debug.SetGCPercent(40)
	go func() {
		for range time.Tick(30 * time.Second) {
			debug.FreeOSMemory()
		}
	}()
	if os.Getenv("GOMAXPROCS") == "" {
		// If GOMAXPROCS hasn't been set, we default to a value of 2 to reduce
		// the number of Go stacks present in the shim.
		runtime.GOMAXPROCS(2)
	}
}

func setLogger(ctx context.Context, id string) (context.Context, error) {
	l := log.G(ctx)
	l.Logger.SetFormatter(&logrus.TextFormatter{
		TimestampFormat: log.RFC3339NanoFixed,
		FullTimestamp:   true,
	})
	if debugFlag {
		l.Logger.SetLevel(log.DebugLevel)
	}
	f, err := openLog(ctx, id)
	if err != nil {
		return ctx, err
	}
	l.Logger.SetOutput(f)
	return log.WithLogger(ctx, l), nil
}

// Run initializes and runs a shim server.
func Run(ctx context.Context, manager Manager, opts ...BinaryOpts) {
	var config Config
	for _, o := range opts {
		o(&config)
	}

	ctx = log.WithLogger(ctx, log.G(ctx).WithField("runtime", manager.Name()))

	if err := run(ctx, manager, config); err != nil {
		fmt.Fprintf(os.Stderr, "%s: %s", manager.Name(), err)
		os.Exit(1)
	}
}

func run(ctx context.Context, manager Manager, config Config) error {
	parseFlags()
	if versionFlag {
		fmt.Printf("%s:\n", filepath.Base(os.Args[0]))
		fmt.Println("  Version: ", version.Version)
		fmt.Println("  Revision:", version.Revision)
		fmt.Println("  Go version:", version.GoVersion)
		fmt.Println("")
		return nil
	}

	if namespaceFlag == "" {
		return fmt.Errorf("shim namespace cannot be empty")
	}

	setRuntime()

	signals, err := setupSignals(config)
	if err != nil {
		return err
	}

	if !config.NoSubreaper {
		if err := subreaper(); err != nil {
			return err
		}
	}

	ttrpcAddress := os.Getenv(ttrpcAddressEnv)
	publisher, err := NewPublisher(ttrpcAddress)
	if err != nil {
		return err
	}
	defer publisher.Close()

	ctx = namespaces.WithNamespace(ctx, namespaceFlag)
	ctx = context.WithValue(ctx, OptsKey{}, Opts{BundlePath: bundlePath, Debug: debugFlag})
	ctx, sd := shutdown.WithShutdown(ctx)
	defer sd.Shutdown()

	// Handle explicit actions
	switch action {
	case "delete":
		logger := log.G(ctx).WithFields(log.Fields{
			"pid":       os.Getpid(),
			"namespace": namespaceFlag,
		})
		if debugFlag {
			logger.Logger.SetLevel(log.DebugLevel)
		}
		go reap(ctx, logger, signals)
		ss, err := manager.Stop(ctx, id)
		if err != nil {
			return err
		}
		data, err := proto.Marshal(&shimapi.DeleteResponse{
			Pid:        uint32(ss.Pid),
			ExitStatus: uint32(ss.ExitStatus),
			ExitedAt:   protobuf.ToTimestamp(ss.ExitedAt),
		})
		if err != nil {
			return err
		}
		if _, err := os.Stdout.Write(data); err != nil {
			return err
		}
		return nil
	case "start":
		opts := StartOpts{
			Address:      addressFlag,
			TTRPCAddress: ttrpcAddress,
			Debug:        debugFlag,
		}

		params, err := manager.Start(ctx, id, opts)
		if err != nil {
			return err
		}

		data, err := json.Marshal(&params)
		if err != nil {
			return fmt.Errorf("failed to marshal bootstrap params to json: %w", err)
		}

		if _, err := os.Stdout.Write(data); err != nil {
			return err
		}

		return nil
	}

	if !config.NoSetupLogger {
		ctx, err = setLogger(ctx, id)
		if err != nil {
			return err
		}
	}

	registry.Register(&plugin.Registration{
		Type: plugins.InternalPlugin,
		ID:   "shutdown",
		InitFn: func(ic *plugin.InitContext) (interface{}, error) {
			return sd, nil
		},
	})

	// Register event plugin
	registry.Register(&plugin.Registration{
		Type: plugins.EventPlugin,
		ID:   "publisher",
		InitFn: func(ic *plugin.InitContext) (interface{}, error) {
			return publisher, nil
		},
	})

	var (
		initialized   = plugin.NewPluginSet()
		ttrpcServices = []TTRPCService{}

		ttrpcUnaryInterceptors = []ttrpc.UnaryServerInterceptor{}
	)

	for _, p := range registry.Graph(func(*plugin.Registration) bool { return false }) {
		pID := p.URI()
		log.G(ctx).WithFields(log.Fields{"id": pID, "type": p.Type}).Info("loading plugin")

		initContext := plugin.NewContext(
			ctx,
			initialized,
			map[string]string{
				// NOTE: Root is empty since the shim does not support persistent storage,
				// shim plugins should make use state directory for writing files to disk.
				// The state directory will be destroyed when the shim if cleaned up or
				// on reboot
				plugins.PropertyStateDir:     filepath.Join(bundlePath, p.URI()),
				plugins.PropertyGRPCAddress:  addressFlag,
				plugins.PropertyTTRPCAddress: ttrpcAddress,
			},
		)

		// load the plugin specific configuration if it is provided
		// TODO: Read configuration passed into shim, or from state directory?
		// if p.Config != nil {
		//	pc, err := config.Decode(p)
		//	if err != nil {
		//		return nil, err
		//	}
		//	initContext.Config = pc
		// }

		result := p.Init(initContext)
		if err := initialized.Add(result); err != nil {
			return fmt.Errorf("could not add plugin result to plugin set: %w", err)
		}

		instance, err := result.Instance()
		if err != nil {
			if plugin.IsSkipPlugin(err) {
				log.G(ctx).WithFields(log.Fields{"id": pID, "type": p.Type, "error": err}).Info("skip loading plugin")
				continue
			}
			return fmt.Errorf("failed to load plugin %s: %w", pID, err)
		}

		if src, ok := instance.(TTRPCService); ok {
			log.G(ctx).WithField("id", pID).Debug("registering ttrpc service")
			ttrpcServices = append(ttrpcServices, src)

		}

		if src, ok := instance.(TTRPCServerOptioner); ok {
			ttrpcUnaryInterceptors = append(ttrpcUnaryInterceptors, src.UnaryInterceptor())
		}
	}

	if len(ttrpcServices) == 0 {
		return fmt.Errorf("required that ttrpc service")
	}

	unaryInterceptor := chainUnaryServerInterceptors(ttrpcUnaryInterceptors...)
	server, err := newServer(ttrpc.WithUnaryServerInterceptor(unaryInterceptor))
	if err != nil {
		return fmt.Errorf("failed creating server: %w", err)
	}

	for _, srv := range ttrpcServices {
		if err := srv.RegisterTTRPC(server); err != nil {
			return fmt.Errorf("failed to register service: %w", err)
		}
	}

	if err := serve(ctx, server, signals, sd.Shutdown); err != nil {
		if !errors.Is(err, shutdown.ErrShutdown) {
			return err
		}
	}

	// NOTE: If the shim server is down(like oom killer), the address
	// socket might be leaking.
	if address, err := ReadAddress("address"); err == nil {
		_ = RemoveSocket(address)
	}

	select {
	case <-sd.Done():
		return nil
	case <-time.After(5 * time.Second):
		return errors.New("shim shutdown timeout")
	}
}

// serve serves the ttrpc API over a unix socket in the current working directory
// and blocks until the context is canceled
func serve(ctx context.Context, server *ttrpc.Server, signals chan os.Signal, shutdown func()) error {
	dump := make(chan os.Signal, 32)
	setupDumpStacks(dump)

	path, err := os.Getwd()
	if err != nil {
		return err
	}

	l, err := serveListener(socketFlag)
	if err != nil {
		return err
	}
	go func() {
		defer l.Close()
		if err := server.Serve(ctx, l); err != nil && !errors.Is(err, net.ErrClosed) {
			log.G(ctx).WithError(err).Fatal("containerd-shim: ttrpc server failure")
		}
	}()
	logger := log.G(ctx).WithFields(log.Fields{
		"pid":       os.Getpid(),
		"path":      path,
		"namespace": namespaceFlag,
	})
	go func() {
		for range dump {
			dumpStacks(logger)
		}
	}()

	go handleExitSignals(ctx, logger, shutdown)
	return reap(ctx, logger, signals)
}

func dumpStacks(logger *log.Entry) {
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
	logger.Infof("=== BEGIN goroutine stack dump ===\n%s\n=== END goroutine stack dump ===", buf)
}
