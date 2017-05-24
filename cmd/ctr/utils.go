package main

import (
	gocontext "context"
	"encoding/csv"
	"fmt"
	"io/ioutil"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/Sirupsen/logrus"
	containersapi "github.com/containerd/containerd/api/services/containers"
	contentapi "github.com/containerd/containerd/api/services/content"
	diffapi "github.com/containerd/containerd/api/services/diff"
	"github.com/containerd/containerd/api/services/execution"
	imagesapi "github.com/containerd/containerd/api/services/images"
	snapshotapi "github.com/containerd/containerd/api/services/snapshot"
	versionservice "github.com/containerd/containerd/api/services/version"
	"github.com/containerd/containerd/api/types/task"
	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/images"
	contentservice "github.com/containerd/containerd/services/content"
	"github.com/containerd/containerd/services/diff"
	imagesservice "github.com/containerd/containerd/services/images"
	snapshotservice "github.com/containerd/containerd/services/snapshot"
	"github.com/containerd/containerd/snapshot"
	specs "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/urfave/cli"
	"google.golang.org/grpc"
)

var grpcConn *grpc.ClientConn

func getContainersService(context *cli.Context) (containersapi.ContainersClient, error) {
	conn, err := getGRPCConnection(context)
	if err != nil {
		return nil, err
	}
	return containersapi.NewContainersClient(conn), nil
}

func getTasksService(context *cli.Context) (execution.TasksClient, error) {
	conn, err := getGRPCConnection(context)
	if err != nil {
		return nil, err
	}
	return execution.NewTasksClient(conn), nil
}

func getContentStore(context *cli.Context) (content.Store, error) {
	conn, err := getGRPCConnection(context)
	if err != nil {
		return nil, err
	}
	return contentservice.NewStoreFromClient(contentapi.NewContentClient(conn)), nil
}

func getSnapshotter(context *cli.Context) (snapshot.Snapshotter, error) {
	conn, err := getGRPCConnection(context)
	if err != nil {
		return nil, err
	}
	return snapshotservice.NewSnapshotterFromClient(snapshotapi.NewSnapshotClient(conn)), nil
}

func getImageStore(clicontext *cli.Context) (images.Store, error) {
	conn, err := getGRPCConnection(clicontext)
	if err != nil {
		return nil, err
	}
	return imagesservice.NewStoreFromClient(imagesapi.NewImagesClient(conn)), nil
}

func getDiffService(context *cli.Context) (diff.DiffService, error) {
	conn, err := getGRPCConnection(context)
	if err != nil {
		return nil, err
	}
	return diff.NewDiffServiceFromClient(diffapi.NewDiffClient(conn)), nil
}

func getVersionService(context *cli.Context) (versionservice.VersionClient, error) {
	conn, err := getGRPCConnection(context)
	if err != nil {
		return nil, err
	}
	return versionservice.NewVersionClient(conn), nil
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

func waitContainer(events execution.Tasks_EventsClient, id string, pid uint32) (uint32, error) {
	for {
		e, err := events.Recv()
		if err != nil {
			return 255, err
		}
		if e.Type != task.Event_EXIT {
			continue
		}
		if e.ID == id && e.Pid == pid {
			return e.ExitStatus, nil
		}
	}
}

func forwardAllSignals(containers execution.TasksClient, id string) chan os.Signal {
	sigc := make(chan os.Signal, 128)
	signal.Notify(sigc)

	go func() {
		for s := range sigc {
			logrus.Debug("Forwarding signal ", s)
			killRequest := &execution.KillRequest{
				ContainerID: id,
				Signal:      uint32(s.(syscall.Signal)),
				PidOrAll: &execution.KillRequest_All{
					All: false,
				},
			}
			_, err := containers.Kill(gocontext.Background(), killRequest)
			if err != nil {
				logrus.Fatalln(err)
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

// parseMountFlag parses a mount string in the form "type=foo,source=/path,destination=/target,options=rbind:rw"
func parseMountFlag(m string) (specs.Mount, error) {
	mount := specs.Mount{}
	r := csv.NewReader(strings.NewReader(m))

	fields, err := r.Read()
	if err != nil {
		return mount, err
	}

	for _, field := range fields {
		v := strings.Split(field, "=")
		if len(v) != 2 {
			return mount, fmt.Errorf("invalid mount specification: expected key=val")
		}

		key := v[0]
		val := v[1]
		switch key {
		case "type":
			mount.Type = val
		case "source", "src":
			mount.Source = val
		case "destination", "dst":
			mount.Destination = val
		case "options":
			mount.Options = strings.Split(val, ":")
		default:
			return mount, fmt.Errorf("mount option %q not supported", key)
		}
	}

	return mount, nil
}

// replaceOrAppendEnvValues returns the defaults with the overrides either
// replaced by env key or appended to the list
func replaceOrAppendEnvValues(defaults, overrides []string) []string {
	cache := make(map[string]int, len(defaults))
	for i, e := range defaults {
		parts := strings.SplitN(e, "=", 2)
		cache[parts[0]] = i
	}

	for _, value := range overrides {
		// Values w/o = means they want this env to be removed/unset.
		if !strings.Contains(value, "=") {
			if i, exists := cache[value]; exists {
				defaults[i] = "" // Used to indicate it should be removed
			}
			continue
		}

		// Just do a normal set/update
		parts := strings.SplitN(value, "=", 2)
		if i, exists := cache[parts[0]]; exists {
			defaults[i] = value
		} else {
			defaults = append(defaults, value)
		}
	}

	// Now remove all entries that we want to "unset"
	for i := 0; i < len(defaults); i++ {
		if defaults[i] == "" {
			defaults = append(defaults[:i], defaults[i+1:]...)
			i--
		}
	}

	return defaults
}
