package main

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/containerd/console"
	contentapi "github.com/containerd/containerd/api/services/content"
	imagesapi "github.com/containerd/containerd/api/services/images"
	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/images"
	"github.com/containerd/containerd/remotes"
	"github.com/containerd/containerd/remotes/docker"
	"github.com/containerd/containerd/rootfs"
	contentservice "github.com/containerd/containerd/services/content"
	imagesservice "github.com/containerd/containerd/services/images"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
	"github.com/urfave/cli"
	"google.golang.org/grpc"
)

var registryFlags = []cli.Flag{
	cli.BoolFlag{
		Name:  "skip-verify,k",
		Usage: "Skip SSL certificate validation",
	},
	cli.BoolFlag{
		Name:  "plain-http",
		Usage: "Allow connections using plain HTTP",
	},
	cli.StringFlag{
		Name:  "user,u",
		Usage: "user[:password] Registry user and password",
	},
	cli.StringFlag{
		Name:  "refresh",
		Usage: "Refresh token for authorization server",
	},
}

func resolveContentStore(context *cli.Context) (content.Store, error) {
	root := filepath.Join(context.GlobalString("root"), "content")
	if !filepath.IsAbs(root) {
		var err error
		root, err = filepath.Abs(root)
		if err != nil {
			return nil, err
		}
	}
	conn, err := connectGRPC(context)
	if err != nil {
		return nil, err
	}

	return contentservice.NewStoreFromClient(contentapi.NewContentClient(conn)), nil
}

func resolveImageStore(clicontext *cli.Context) (images.Store, error) {
	conn, err := connectGRPC(clicontext)
	if err != nil {
		return nil, err
	}
	return imagesservice.NewStoreFromClient(imagesapi.NewImagesClient(conn)), nil
}

func connectGRPC(context *cli.Context) (*grpc.ClientConn, error) {
	address := context.GlobalString("address")
	timeout := context.GlobalDuration("connect-timeout")
	return grpc.Dial(address,
		grpc.WithTimeout(timeout),
		grpc.WithBlock(),
		grpc.WithInsecure(),
		grpc.WithDialer(func(addr string, timeout time.Duration) (net.Conn, error) {
			return net.DialTimeout("unix", address, timeout)
		}),
	)
}

// getResolver prepares the resolver from the environment and options.
func getResolver(ctx context.Context, clicontext *cli.Context) (remotes.Resolver, error) {
	username := clicontext.String("user")
	var secret string
	if i := strings.IndexByte(username, ':'); i > 0 {
		secret = username[i+1:]
		username = username[0:i]
	}
	options := docker.ResolverOptions{
		PlainHTTP: clicontext.Bool("plain-http"),
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

func getImageLayers(ctx context.Context, image images.Image, cs content.Store) ([]rootfs.Layer, error) {
	p, err := content.ReadBlob(ctx, cs, image.Target.Digest)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to read manifest blob")
	}

	var manifest ocispec.Manifest
	if err := json.Unmarshal(p, &manifest); err != nil {
		return nil, errors.Wrap(err, "failed to unmarshal manifest")
	}

	diffIDs, err := image.RootFS(ctx, cs)
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
