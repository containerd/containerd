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
	gocontext "context"
	"os"
	"strconv"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/pkg/epoch"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/containerd/log"
	"github.com/urfave/cli/v2"
)

// AppContext returns the context for a command. Should only be called once per
// command, near the start.
//
// This will ensure the namespace is picked up and set the timeout, if one is
// defined.
func AppContext(context *cli.Context) (gocontext.Context, gocontext.CancelFunc) {
	var (
		ctx       = gocontext.Background()
		timeout   = context.Duration("timeout")
		namespace = context.String("namespace")
		cancel    gocontext.CancelFunc
	)
	ctx = namespaces.WithNamespace(ctx, namespace)
	if timeout > 0 {
		ctx, cancel = gocontext.WithTimeout(ctx, timeout)
	} else {
		ctx, cancel = gocontext.WithCancel(ctx)
	}
	if tm, err := epoch.SourceDateEpoch(); err != nil {
		log.L.WithError(err).Warn("Failed to read SOURCE_DATE_EPOCH")
	} else if tm != nil {
		log.L.Debugf("Using SOURCE_DATE_EPOCH: %v", tm)
		ctx = epoch.WithSourceDateEpoch(ctx, tm)
	}
	return ctx, cancel
}

// NewClient returns a new containerd client
func NewClient(context *cli.Context, opts ...containerd.Opt) (*containerd.Client, gocontext.Context, gocontext.CancelFunc, error) {
	timeoutOpt := containerd.WithTimeout(context.Duration("connect-timeout"))
	opts = append(opts, timeoutOpt)
	client, err := containerd.New(context.String("address"), opts...)
	if err != nil {
		return nil, nil, nil, err
	}
	ctx, cancel := AppContext(context)
	var suppressDeprecationWarnings bool
	if s := os.Getenv("CONTAINERD_SUPPRESS_DEPRECATION_WARNINGS"); s != "" {
		suppressDeprecationWarnings, err = strconv.ParseBool(s)
		if err != nil {
			log.L.WithError(err).Warn("Failed to parse CONTAINERD_SUPPRESS_DEPRECATION_WARNINGS=" + s)
		}
	}
	if !suppressDeprecationWarnings {
		resp, err := client.IntrospectionService().Server(ctx)
		if err != nil {
			log.L.WithError(err).Warn("Failed to check deprecations")
		} else {
			for _, d := range resp.Deprecations {
				log.L.Warn("DEPRECATION: " + d.Message)
			}
		}
	}
	return client, ctx, cancel, nil
}
