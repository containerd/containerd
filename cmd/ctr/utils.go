package main

import (
	"bufio"
	gocontext "context"
	"crypto/tls"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/containerd/console"
	"github.com/containerd/containerd"
	containersapi "github.com/containerd/containerd/api/services/containers/v1"
	contentapi "github.com/containerd/containerd/api/services/content/v1"
	diffapi "github.com/containerd/containerd/api/services/diff/v1"
	"github.com/containerd/containerd/api/services/events/v1"
	imagesapi "github.com/containerd/containerd/api/services/images/v1"
	namespacesapi "github.com/containerd/containerd/api/services/namespaces/v1"
	snapshotapi "github.com/containerd/containerd/api/services/snapshot/v1"
	"github.com/containerd/containerd/api/services/tasks/v1"
	versionservice "github.com/containerd/containerd/api/services/version/v1"
	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/images"
	"github.com/containerd/containerd/namespaces"
	"github.com/containerd/containerd/platforms"
	"github.com/containerd/containerd/remotes"
	"github.com/containerd/containerd/remotes/docker"
	"github.com/containerd/containerd/rootfs"
	contentservice "github.com/containerd/containerd/services/content"
	"github.com/containerd/containerd/services/diff"
	imagesservice "github.com/containerd/containerd/services/images"
	namespacesservice "github.com/containerd/containerd/services/namespaces"
	snapshotservice "github.com/containerd/containerd/services/snapshot"
	"github.com/containerd/containerd/snapshot"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	specs "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/urfave/cli"
	"google.golang.org/grpc"
)

var (
	snapshotterFlags = []cli.Flag{
		cli.StringFlag{
			Name:  "snapshotter",
			Usage: "snapshotter name. Empty value stands for the daemon default value.",
			Value: containerd.DefaultSnapshotter,
		},
	}

	registryFlags = []cli.Flag{
		cli.BoolFlag{
			Name:  "skip-verify,k",
			Usage: "skip SSL certificate validation",
		},
		cli.BoolFlag{
			Name:  "plain-http",
			Usage: "allow connections using plain HTTP",
		},
		cli.StringFlag{
			Name:  "user,u",
			Usage: "user[:password] Registry user and password",
		},
		cli.StringFlag{
			Name:  "refresh",
			Usage: "refresh token for authorization server",
		},
	}
)

var grpcConn *grpc.ClientConn

// appContext returns the context for a command. Should only be called once per
// command, near the start.
//
// This will ensure the namespace is picked up and set the timeout, if one is
// defined.
func appContext(clicontext *cli.Context) (gocontext.Context, gocontext.CancelFunc) {
	var (
		ctx       = gocontext.Background()
		timeout   = clicontext.GlobalDuration("timeout")
		namespace = clicontext.GlobalString("namespace")
		cancel    gocontext.CancelFunc
	)

	ctx = namespaces.WithNamespace(ctx, namespace)

	if timeout > 0 {
		ctx, cancel = gocontext.WithTimeout(ctx, timeout)
	} else {
		ctx, cancel = gocontext.WithCancel(ctx)
	}

	return ctx, cancel
}

func getNamespacesService(clicontext *cli.Context) (namespaces.Store, error) {
	conn, err := getGRPCConnection(clicontext)
	if err != nil {
		return nil, err
	}
	return namespacesservice.NewStoreFromClient(namespacesapi.NewNamespacesClient(conn)), nil
}

func newClient(context *cli.Context) (*containerd.Client, error) {
	return containerd.New(context.GlobalString("address"))
}

func getContainersService(context *cli.Context) (containersapi.ContainersClient, error) {
	conn, err := getGRPCConnection(context)
	if err != nil {
		return nil, err
	}
	return containersapi.NewContainersClient(conn), nil
}

func getTasksService(context *cli.Context) (tasks.TasksClient, error) {
	conn, err := getGRPCConnection(context)
	if err != nil {
		return nil, err
	}
	return tasks.NewTasksClient(conn), nil
}

func getEventsService(context *cli.Context) (events.EventsClient, error) {
	conn, err := getGRPCConnection(context)
	if err != nil {
		return nil, err
	}

	return events.NewEventsClient(conn), nil
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
	return snapshotservice.NewSnapshotterFromClient(snapshotapi.NewSnapshotsClient(conn), context.GlobalString("snapshotter")), nil
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

func passwordPrompt() (string, error) {
	c := console.Current()
	defer c.Reset()

	if err := c.DisableEcho(); err != nil {
		return "", errors.Wrap(err, "failed to disable echo")
	}

	line, _, err := bufio.NewReader(c).ReadLine()
	if err != nil {
		return "", errors.Wrap(err, "failed to read line")
	}
	return string(line), nil
}

func getImageLayers(ctx gocontext.Context, image images.Image, cs content.Store) ([]rootfs.Layer, error) {
	p, err := content.ReadBlob(ctx, cs, image.Target.Digest)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to read manifest blob")
	}

	var manifest ocispec.Manifest
	if err := json.Unmarshal(p, &manifest); err != nil {
		return nil, errors.Wrap(err, "failed to unmarshal manifest")
	}

	diffIDs, err := image.RootFS(ctx, cs, platforms.Default())
	if err != nil {
		return nil, errors.Wrap(err, "failed to resolve rootfs")
	}

	if len(diffIDs) != len(manifest.Layers) {
		return nil, errors.Errorf("mismatched image rootfs and manifest layers")
	}

	layers := make([]rootfs.Layer, len(diffIDs))
	for i := range diffIDs {
		layers[i].Diff = ocispec.Descriptor{
			// TODO: derive media type from compressed type
			MediaType: ocispec.MediaTypeImageLayer,
			Digest:    diffIDs[i],
		}
		layers[i].Blob = manifest.Layers[i]
	}

	return layers, nil
}

// getResolver prepares the resolver from the environment and options.
func getResolver(ctx gocontext.Context, clicontext *cli.Context) (remotes.Resolver, error) {
	username := clicontext.String("user")
	var secret string
	if i := strings.IndexByte(username, ':'); i > 0 {
		secret = username[i+1:]
		username = username[0:i]
	}
	options := docker.ResolverOptions{
		PlainHTTP: clicontext.Bool("plain-http"),
		Tracker:   pushTracker,
	}
	if username != "" {
		if secret == "" {
			fmt.Printf("Password: ")

			var err error
			secret, err = passwordPrompt()
			if err != nil {
				return nil, err
			}

			fmt.Print("\n")
		}
	} else if rt := clicontext.String("refresh"); rt != "" {
		secret = rt
	}

	options.Credentials = func(host string) (string, string, error) {
		// Only one host
		return username, secret, nil
	}

	tr := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
			DualStack: true,
		}).DialContext,
		MaxIdleConns:        10,
		IdleConnTimeout:     30 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: clicontext.Bool("insecure"),
		},
		ExpectContinueTimeout: 5 * time.Second,
	}

	options.Client = &http.Client{
		Transport: tr,
	}

	return docker.NewResolver(options), nil
}

func forwardAllSignals(ctx gocontext.Context, task killer) chan os.Signal {
	sigc := make(chan os.Signal, 128)
	signal.Notify(sigc)
	go func() {
		for s := range sigc {
			logrus.Debug("forwarding signal ", s)
			if err := task.Kill(ctx, s.(syscall.Signal)); err != nil {
				logrus.WithError(err).Errorf("forward signal %s", s)
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

func objectWithLabelArgs(clicontext *cli.Context) (string, map[string]string) {
	var (
		namespace    = clicontext.Args().First()
		labelStrings = clicontext.Args().Tail()
	)

	return namespace, labelArgs(labelStrings)
}

func labelArgs(labelStrings []string) map[string]string {
	labels := make(map[string]string, len(labelStrings))
	for _, label := range labelStrings {
		parts := strings.SplitN(label, "=", 2)
		key := parts[0]
		value := "true"
		if len(parts) > 1 {
			value = parts[1]
		}

		labels[key] = value
	}

	return labels
}

func printAsJSON(x interface{}) {
	b, err := json.MarshalIndent(x, "", "    ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "can't marshal %+v as a JSON string: %v\n", x, err)
	}
	fmt.Println(string(b))
}
