package main

import (
	gocontext "context"
	"fmt"
	"io/ioutil"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/Sirupsen/logrus"
	contentapi "github.com/containerd/containerd/api/services/content"
	"github.com/containerd/containerd/api/services/execution"
	imagesapi "github.com/containerd/containerd/api/services/images"
	rootfsapi "github.com/containerd/containerd/api/services/rootfs"
	"github.com/containerd/containerd/api/types/container"
	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/images"
	contentservice "github.com/containerd/containerd/services/content"
	imagesservice "github.com/containerd/containerd/services/images"
	"github.com/urfave/cli"
	"google.golang.org/grpc"
)

var grpcConn *grpc.ClientConn

func getExecutionService(context *cli.Context) (execution.ContainerServiceClient, error) {
	conn, err := getGRPCConnection(context)
	if err != nil {
		return nil, err
	}
	return execution.NewContainerServiceClient(conn), nil
}

func getContentProvider(context *cli.Context) (content.Provider, error) {
	conn, err := getGRPCConnection(context)
	if err != nil {
		return nil, err
	}
	return contentservice.NewProviderFromClient(contentapi.NewContentClient(conn)), nil
}

func getRootFSService(context *cli.Context) (rootfsapi.RootFSClient, error) {
	conn, err := getGRPCConnection(context)
	if err != nil {
		return nil, err
	}
	return rootfsapi.NewRootFSClient(conn), nil
}

func getImageStore(clicontext *cli.Context) (images.Store, error) {
	conn, err := getGRPCConnection(clicontext)
	if err != nil {
		return nil, err
	}
	return imagesservice.NewStoreFromClient(imagesapi.NewImagesClient(conn)), nil
}

func getTempDir(id string) (string, error) {
	err := os.MkdirAll(filepath.Join(os.TempDir(), "ctr"), 0700)
	if err != nil {
		return "", err
	}
	tmpDir, err := ioutil.TempDir(filepath.Join(os.TempDir(), "ctr"), fmt.Sprintf("%s-", id))
	if err != nil {
		return "", err
	}
	return tmpDir, nil
}

func waitContainer(events execution.ContainerService_EventsClient, id string, pid uint32) (uint32, error) {
	for {
		e, err := events.Recv()
		if err != nil {
			return 255, err
		}
		if e.Type != container.Event_EXIT {
			continue
		}
		if e.ID == id && e.Pid == pid {
			return e.ExitStatus, nil
		}
	}
}

func forwardAllSignals(containers execution.ContainerServiceClient, id string) chan os.Signal {
	sigc := make(chan os.Signal, 128)
	signal.Notify(sigc)

	go func() {
		for s := range sigc {
			logrus.Debug("Forwarding signal ", s)
			killRequest := &execution.KillRequest{
				ID:     id,
				Signal: uint32(s.(syscall.Signal)),
				All:    false,
			}
			_, err := containers.Kill(gocontext.Background(), killRequest)
			if err != nil {
				logrus.WithError(err).Error("grpc event from kill")
			}
		}
	}()
	return sigc
}

func parseSignal(rawSignal string) (syscall.Signal, error) {
	s, err := strconv.Atoi(rawSignal)
	if err == nil {
		sig := syscall.Signal(s)
		for _, msig := range signalMap {
			if sig == msig {
				return sig, nil
			}
		}
		return -1, fmt.Errorf("unknown signal %q", rawSignal)
	}
	signal, ok := signalMap[strings.TrimPrefix(strings.ToUpper(rawSignal), "SIG")]
	if !ok {
		return -1, fmt.Errorf("unknown signal %q", rawSignal)
	}
	return signal, nil
}

func stopCatch(sigc chan os.Signal) {
	signal.Stop(sigc)
	close(sigc)
}
