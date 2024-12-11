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

package commands

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/containerd/console"
	"github.com/containerd/containerd/v2/core/remotes"
	"github.com/containerd/containerd/v2/core/remotes/docker"
	"github.com/containerd/containerd/v2/core/remotes/docker/config"
	"github.com/containerd/containerd/v2/core/transfer/registry"
	"github.com/containerd/containerd/v2/pkg/httpdbg"
	"github.com/urfave/cli/v2"
)

// PushTracker returns a new InMemoryTracker which tracks the ref status
var PushTracker = docker.NewInMemoryTracker()

func passwordPrompt() (string, error) {
	c := console.Current()
	defer c.Reset()

	if err := c.DisableEcho(); err != nil {
		return "", fmt.Errorf("failed to disable echo: %w", err)
	}

	line, _, err := bufio.NewReader(c).ReadLine()
	if err != nil {
		return "", fmt.Errorf("failed to read line: %w", err)
	}
	return string(line), nil
}

// GetResolver prepares the resolver from the environment and options
func GetResolver(ctx context.Context, cliContext *cli.Context) (remotes.Resolver, error) {
	username := cliContext.String("user")
	var secret string
	if i := strings.IndexByte(username, ':'); i > 0 {
		secret = username[i+1:]
		username = username[0:i]
	}
	options := docker.ResolverOptions{
		Tracker: PushTracker,
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
	} else if rt := cliContext.String("refresh"); rt != "" {
		secret = rt
	}

	hostOptions := config.HostOptions{}
	hostOptions.Credentials = func(host string) (string, string, error) {
		// If host doesn't match...
		// Only one host
		return username, secret, nil
	}
	if cliContext.Bool("plain-http") {
		hostOptions.DefaultScheme = "http"
	}
	defaultTLS, err := resolverDefaultTLS(cliContext)
	if err != nil {
		return nil, err
	}
	hostOptions.DefaultTLS = defaultTLS
	if hostDir := cliContext.String("hosts-dir"); hostDir != "" {
		hostOptions.HostDir = config.HostDirFromRoot(hostDir)
	}

	if cliContext.Bool("http-dump") {
		hostOptions.UpdateClient = func(client *http.Client) error {
			httpdbg.DumpRequests(ctx, client, nil)
			return nil
		}
	}

	options.Hosts = config.ConfigureHosts(ctx, hostOptions)

	return docker.NewResolver(options), nil
}

func resolverDefaultTLS(cliContext *cli.Context) (*tls.Config, error) {
	tlsConfig := &tls.Config{}

	if cliContext.Bool("skip-verify") {
		tlsConfig.InsecureSkipVerify = true
	}

	if tlsRootPath := cliContext.String("tlscacert"); tlsRootPath != "" {
		tlsRootData, err := os.ReadFile(tlsRootPath)
		if err != nil {
			return nil, fmt.Errorf("failed to read %q: %w", tlsRootPath, err)
		}

		tlsConfig.RootCAs = x509.NewCertPool()
		if !tlsConfig.RootCAs.AppendCertsFromPEM(tlsRootData) {
			return nil, fmt.Errorf("failed to load TLS CAs from %q: invalid data", tlsRootPath)
		}
	}

	tlsCertPath := cliContext.String("tlscert")
	tlsKeyPath := cliContext.String("tlskey")
	if tlsCertPath != "" || tlsKeyPath != "" {
		if tlsCertPath == "" || tlsKeyPath == "" {
			return nil, errors.New("flags --tlscert and --tlskey must be set together")
		}
		keyPair, err := tls.LoadX509KeyPair(tlsCertPath, tlsKeyPath)
		if err != nil {
			return nil, fmt.Errorf("failed to load TLS client credentials (cert=%q, key=%q): %w", tlsCertPath, tlsKeyPath, err)
		}
		tlsConfig.Certificates = []tls.Certificate{keyPair}
	}

	// If nothing was set, return nil rather than empty config
	if !tlsConfig.InsecureSkipVerify && tlsConfig.RootCAs == nil && tlsConfig.Certificates == nil {
		return nil, nil
	}

	return tlsConfig, nil
}

type staticCredentials struct {
	ref      string
	username string
	secret   string
}

// NewStaticCredentials gets credentials from passing in cli context
func NewStaticCredentials(ctx context.Context, cliContext *cli.Context, ref string) (registry.CredentialHelper, error) {
	username := cliContext.String("user")
	var secret string
	if i := strings.IndexByte(username, ':'); i > 0 {
		secret = username[i+1:]
		username = username[0:i]
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
	} else if rt := cliContext.String("refresh"); rt != "" {
		secret = rt
	}

	return &staticCredentials{
		ref:      ref,
		username: username,
		secret:   secret,
	}, nil
}

func (sc *staticCredentials) GetCredentials(ctx context.Context, ref, host string) (registry.Credentials, error) {
	if ref == sc.ref {
		return registry.Credentials{
			Username: sc.username,
			Secret:   sc.secret,
		}, nil
	}
	return registry.Credentials{}, nil
}
