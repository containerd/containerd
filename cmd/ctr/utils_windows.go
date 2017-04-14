package main

import (
	"io"
	"io/ioutil"
	"log"
	"net"
	"os"
	"sync"
	"syscall"
	"time"

	"github.com/Microsoft/go-winio"
	clog "github.com/containerd/containerd/log"
	"github.com/pkg/errors"
	"github.com/urfave/cli"
	"golang.org/x/sys/windows"
	"google.golang.org/grpc"
	"google.golang.org/grpc/grpclog"
)

func getGRPCConnection(context *cli.Context) (*grpc.ClientConn, error) {
	if grpcConn != nil {
		return grpcConn, nil
	}

	bindAddress := context.GlobalString("address")
	// reset the logger for grpc to log to dev/null so that it does not mess with our stdio
	grpclog.SetLogger(log.New(ioutil.Discard, "", log.LstdFlags))
	dialOpts := []grpc.DialOption{grpc.WithInsecure(), grpc.WithTimeout(100 * time.Second)}
	dialOpts = append(dialOpts,
		grpc.WithDialer(func(addr string, timeout time.Duration) (net.Conn, error) {
			return winio.DialPipe(bindAddress, &timeout)
		},
		))

	conn, err := grpc.Dial(bindAddress, dialOpts...)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to dial %q", bindAddress)
	}

	grpcConn = conn
	return grpcConn, nil
}

func prepareStdio(stdin, stdout, stderr string, console bool) (*sync.WaitGroup, error) {
	var wg sync.WaitGroup

	if stdin != "" {
		l, err := winio.ListenPipe(stdin, nil)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to create stdin pipe %s", stdin)
		}
		defer func(l net.Listener) {
			if err != nil {
				l.Close()
			}
		}(l)

		go func() {
			c, err := l.Accept()
			if err != nil {
				clog.L.WithError(err).Errorf("failed to accept stdin connection on %s", stdin)
				return
			}
			io.Copy(c, os.Stdin)
			c.Close()
			l.Close()
		}()
	}

	if stdout != "" {
		l, err := winio.ListenPipe(stdout, nil)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to create stdin pipe %s", stdout)
		}
		defer func(l net.Listener) {
			if err != nil {
				l.Close()
			}
		}(l)

		wg.Add(1)
		go func() {
			defer wg.Done()
			c, err := l.Accept()
			if err != nil {
				clog.L.WithError(err).Errorf("failed to accept stdout connection on %s", stdout)
				return
			}
			io.Copy(os.Stdout, c)
			c.Close()
			l.Close()
		}()
	}

	if !console && stderr != "" {
		l, err := winio.ListenPipe(stderr, nil)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to create stderr pipe %s", stderr)
		}
		defer func(l net.Listener) {
			if err != nil {
				l.Close()
			}
		}(l)

		wg.Add(1)
		go func() {
			defer wg.Done()
			c, err := l.Accept()
			if err != nil {
				clog.L.WithError(err).Errorf("failed to accept stderr connection on %s", stderr)
				return
			}
			io.Copy(os.Stderr, c)
			c.Close()
			l.Close()
		}()
	}

	return &wg, nil
}

var signalMap = map[string]syscall.Signal{
	"HUP":    syscall.Signal(windows.SIGHUP),
	"INT":    syscall.Signal(windows.SIGINT),
	"QUIT":   syscall.Signal(windows.SIGQUIT),
	"SIGILL": syscall.Signal(windows.SIGILL),
	"TRAP":   syscall.Signal(windows.SIGTRAP),
	"ABRT":   syscall.Signal(windows.SIGABRT),
	"BUS":    syscall.Signal(windows.SIGBUS),
	"FPE":    syscall.Signal(windows.SIGFPE),
	"KILL":   syscall.Signal(windows.SIGKILL),
	"SEGV":   syscall.Signal(windows.SIGSEGV),
	"PIPE":   syscall.Signal(windows.SIGPIPE),
	"ALRM":   syscall.Signal(windows.SIGALRM),
	"TERM":   syscall.Signal(windows.SIGTERM),
}
