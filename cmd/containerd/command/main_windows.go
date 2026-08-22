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

package command

import (
	"context"
	"os"

	"github.com/Microsoft/go-winio/pkg/etw"
	"github.com/Microsoft/go-winio/pkg/etwlogrus"
	"github.com/Microsoft/go-winio/pkg/guid"
	"github.com/containerd/containerd/v2/cmd/containerd/server"
	"github.com/containerd/containerd/v2/internal/stackdump"
	"github.com/containerd/log"
	"github.com/sirupsen/logrus"
)

var (
	handledSignals = []os.Signal{os.Interrupt}
)

func handleSignals(ctx context.Context, signals chan os.Signal, serverC chan *server.Server, cancel func()) chan struct{} {
	done := make(chan struct{})
	go func() {
		var server *server.Server
		for {
			select {
			case s := <-serverC:
				server = s
			case s := <-signals:
				log.G(ctx).WithField("signal", s).Debug("received signal")

				if err := notifyStopping(ctx); err != nil {
					log.G(ctx).WithError(err).Error("notify stopping failed")
				}

				cancel()
				if server != nil {
					server.Stop()
				}
				close(done)
				return
			}
		}
	}()
	setupDumpStacks()
	return done
}

// setupDumpStacks dumps stacks whenever this process's stackdump event is
// signaled. Windows has no SIGUSR1 to trap, so the named event from
// internal/stackdump stands in for it.
func setupDumpStacks() {
	if err := stackdump.Notify(func() { dumpStacks(true) }); err != nil {
		log.L.WithError(err).Error("failed to set up debug stackdump event, stack dumps unavailable")
	}
}

func etwCallback(sourceID guid.GUID, state etw.ProviderState, level etw.Level, matchAnyKeyword uint64, matchAllKeyword uint64, filterData uintptr) {
	if state == etw.ProviderStateCaptureState {
		dumpStacks(false)
	}
}

func init() {
	// Provider ID: 2acb92c0-eb9b-571a-69cf-8f3410f383ad
	// Provider and hook aren't closed explicitly, as they will exist until
	// process exit. GUID is generated based on name - see
	// Microsoft/go-winio/tools/etw-provider-gen.
	provider, err := etw.NewProvider("ContainerD", etwCallback)
	if err != nil {
		log.L.Error(err)
	} else {
		if hook, err := etwlogrus.NewHookFromProvider(provider); err == nil {
			logrus.AddHook(hook)
		} else {
			log.L.Error(err)
		}
	}
}
