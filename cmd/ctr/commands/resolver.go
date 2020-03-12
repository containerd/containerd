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
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/containerd/console"
	"github.com/containerd/containerd/remotes"
	"github.com/containerd/containerd/remotes/certutil"
	"github.com/containerd/containerd/remotes/docker"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/urfave/cli"
	"strings"
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
	var (
		secret string
		opts   []docker.RegistryOpt
	)
	username := clicontext.String("user")
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
	} else if rt := clicontext.String("refresh"); rt != "" {
		secret = rt
	}

	credentials := func(host string) (string, string, error) {
		// Only one host
		return username, secret, nil
	}
	loadCertsDir(tr.TLSClientConfig, clicontext.String("certs-dir"))

	if clicontext.Bool("plain-http") {
		opts = append(opts, docker.WithPlainHTTP(docker.MatchAllHosts))
	}

	opts = append(opts,
		docker.WithSkipVerify(clicontext.Bool("skip-verify")),
		docker.WithCertRootDir(clicontext.String("cert-dir")),
	)

	authOpts := []docker.AuthorizerOpt{docker.WithAuthCreds(credentials)}

	opts = append(opts, docker.WithAuthorizer(docker.NewDockerAuthorizer(authOpts...)))
	options := docker.ResolverOptions{
		Hosts:   docker.ConfigureDefaultRegistries(opts...),
		Tracker: PushTracker,
	}

	return docker.NewResolver(options), nil
}

// loadCertsDir loads certs from certsDir like "/etc/docker/certs.d" .
func loadCertsDir(config *tls.Config, certsDir string) {
	if certsDir == "" {
		return
	}
	fs, err := ioutil.ReadDir(certsDir)
	if err != nil && !os.IsNotExist(err) {
		logrus.WithError(err).Errorf("cannot read certs directory %q", certsDir)
		return
	}
	for _, f := range fs {
		if !f.IsDir() {
			continue
		}
		// TODO: skip loading if f.Name() is not valid FQDN/IP
		hostDir := filepath.Join(certsDir, f.Name())
		caCertGlob := filepath.Join(hostDir, "*.crt")
		if _, err = certutil.LoadCACerts(config, caCertGlob); err != nil {
			logrus.WithError(err).Errorf("cannot load certs from %q", caCertGlob)
		}
		keyGlob := filepath.Join(hostDir, "*.key")
		if _, _, err = certutil.LoadKeyPairs(config, keyGlob, certutil.DockerKeyPairCertLocator); err != nil {
			logrus.WithError(err).Errorf("cannot load key pairs from %q", keyGlob)
		}
	}
}
