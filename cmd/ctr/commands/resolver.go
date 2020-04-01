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
	gocontext "context"
	"crypto/tls"
	"fmt"
	"strings"

	"github.com/containerd/console"
	"github.com/containerd/containerd/remotes"
	"github.com/containerd/containerd/remotes/docker"
	"github.com/containerd/containerd/remotes/docker/config"
	"github.com/pkg/errors"
	"github.com/urfave/cli"
)

// PushTracker returns a new InMemoryTracker which tracks the ref status
var PushTracker = docker.NewInMemoryTracker()

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

// GetResolver prepares the resolver from the environment and options
func GetResolver(ctx gocontext.Context, clicontext *cli.Context) (remotes.Resolver, error) {
	username := clicontext.String("user")
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
	} else if rt := clicontext.String("refresh"); rt != "" {
		secret = rt
	}

	hostOptions := config.HostOptions{}
	hostOptions.Credentials = func(host string) (string, string, error) {
		// If host doesn't match...
		// Only one host
		return username, secret, nil
	}
	if clicontext.Bool("plain-http") {
		hostOptions.DefaultScheme = "http"
	}

	if clicontext.Bool("skip-verify") {
		hostOptions.DefaultTLS = &tls.Config{
			InsecureSkipVerify: true,
		}
	}
	if hostDir := clicontext.String("hosts-dir"); hostDir != "" {
		hostOptions.HostDir = config.HostDirFromRoot(hostDir)
	}

	options.Hosts = config.ConfigureHosts(ctx, hostOptions)

	return docker.NewResolver(options), nil
}
