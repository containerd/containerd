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

package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/oci"
	"github.com/containerd/containerd/pkg/cri/streaming"
	"github.com/containerd/containerd/plugin"
	cni "github.com/containerd/go-cni"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"

	"github.com/containerd/containerd/pkg/atomic"
	criconfig "github.com/containerd/containerd/pkg/cri/config"
	cristore "github.com/containerd/containerd/pkg/cri/store/service"
	snapshotstore "github.com/containerd/containerd/pkg/cri/store/snapshot"
	ctrdutil "github.com/containerd/containerd/pkg/cri/util"
	osinterface "github.com/containerd/containerd/pkg/os"
	"github.com/containerd/containerd/pkg/registrar"
)

// criService implements CRIService.
type criService struct {
	// stores all resources associated with cri
	*cristore.Store
	// config contains all configurations.
	config *criconfig.Config
	// imageFSPath is the path to image filesystem.
	imageFSPath string
	// os is an interface for all required os operations.
	os osinterface.OS
	// sandboxNameIndex stores all sandbox names and make sure each name
	// is unique.
	sandboxNameIndex *registrar.Registrar
	// containerNameIndex stores all container names and make sure each
	// name is unique.
	containerNameIndex *registrar.Registrar
	// snapshotStore stores information of all snapshots.
	snapshotStore *snapshotstore.Store
	// netPlugin is used to setup and teardown network when run/stop pod sandbox.
	netPlugin cni.CNI
	// client is an instance of the containerd client
	client *containerd.Client
	// streamServer is the streaming server serves container streaming request.
	streamServer streaming.Server
	// eventMonitor is the monitor monitors containerd events.
	eventMonitor *eventMonitor
	// initialized indicates whether the server is initialized. All GRPC services
	// should return error before the server is initialized.
	initialized atomic.Bool
	// cniNetConfMonitor is used to reload cni network conf if there is
	// any valid fs change events from cni network conf dir.
	cniNetConfMonitor *cniNetConfSyncer
	// baseOCISpecs contains cached OCI specs loaded via `Runtime.BaseRuntimeSpec`
	baseOCISpecs map[string]*oci.Spec
	// allCaps is the list of the capabilities.
	// When nil, parsed from CapEff of /proc/self/status.
	allCaps []string // nolint
}

// NewCRIService returns a new instance of CRIPlugin, it's the default implementions of CRIPlugin
func newCRIService(config *criconfig.Config, client *containerd.Client, store *cristore.Store) (*criService, error) {
	var err error
	c := &criService{
		Store:              store,
		config:             config,
		client:             client,
		os:                 osinterface.RealOS{},
		snapshotStore:      snapshotstore.NewStore(),
		sandboxNameIndex:   registrar.NewRegistrar(),
		containerNameIndex: registrar.NewRegistrar(),
		initialized:        atomic.NewBool(false),
	}

	if client.SnapshotService(c.config.ContainerdConfig.Snapshotter) == nil {
		return nil, errors.Errorf("failed to find snapshotter %q", c.config.ContainerdConfig.Snapshotter)
	}

	c.imageFSPath = imageFSPath(config.ContainerdRootDir, config.ContainerdConfig.Snapshotter)
	logrus.Infof("Get image filesystem path %q", c.imageFSPath)

	if err := c.initPlatform(); err != nil {
		return nil, errors.Wrap(err, "initialize platform")
	}

	// prepare streaming server
	c.streamServer, err = newStreamServer(c, config.StreamServerAddress, config.StreamServerPort, config.StreamIdleTimeout)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create stream server")
	}

	c.eventMonitor = newEventMonitor(c)

	c.cniNetConfMonitor, err = newCNINetConfSyncer(c.config.NetworkPluginConfDir, c.netPlugin, c.cniLoadOptions())
	if err != nil {
		return nil, errors.Wrap(err, "failed to create cni conf monitor")
	}

	// Preload base OCI specs
	c.baseOCISpecs, err = loadBaseOCISpecs(config)
	if err != nil {
		return nil, err
	}

	return c, nil
}

// implement CRIPlugin Initialized interface
func (c *criService) Initialized() bool {
	return c.initialized.IsSet()
}

// Run starts the CRI service.
func (c *criService) Run() error {
	logrus.Info("Start subscribing containerd event")
	c.eventMonitor.subscribe(c.client)

	logrus.Infof("Start recovering state")
	if err := c.recover(ctrdutil.NamespacedContext()); err != nil {
		return errors.Wrap(err, "failed to recover state")
	}

	// Start event handler.
	logrus.Info("Start event monitor")
	eventMonitorErrCh := c.eventMonitor.start()

	// Start snapshot stats syncer, it doesn't need to be stopped.
	logrus.Info("Start snapshots syncer")
	snapshotsSyncer := newSnapshotsSyncer(
		c.snapshotStore,
		c.client.SnapshotService(c.config.ContainerdConfig.Snapshotter),
		time.Duration(c.config.StatsCollectPeriod)*time.Second,
	)
	snapshotsSyncer.start()

	// Start CNI network conf syncer
	logrus.Info("Start cni network conf syncer")
	cniNetConfMonitorErrCh := make(chan error, 1)
	go func() {
		defer close(cniNetConfMonitorErrCh)
		cniNetConfMonitorErrCh <- c.cniNetConfMonitor.syncLoop()
	}()

	// Start streaming server.
	logrus.Info("Start streaming server")
	streamServerErrCh := make(chan error)
	go func() {
		defer close(streamServerErrCh)
		if err := c.streamServer.Start(true); err != nil && err != http.ErrServerClosed {
			logrus.WithError(err).Error("Failed to start streaming server")
			streamServerErrCh <- err
		}
	}()

	// Set the server as initialized. GRPC services could start serving traffic.
	c.initialized.Set()

	var eventMonitorErr, streamServerErr, cniNetConfMonitorErr error
	// Stop the whole CRI service if any of the critical service exits.
	select {
	case eventMonitorErr = <-eventMonitorErrCh:
	case streamServerErr = <-streamServerErrCh:
	case cniNetConfMonitorErr = <-cniNetConfMonitorErrCh:
	}
	if err := c.Close(); err != nil {
		return errors.Wrap(err, "failed to stop cri service")
	}
	// If the error is set above, err from channel must be nil here, because
	// the channel is supposed to be closed. Or else, we wait and set it.
	if err := <-eventMonitorErrCh; err != nil {
		eventMonitorErr = err
	}
	logrus.Info("Event monitor stopped")
	// There is a race condition with http.Server.Serve.
	// When `Close` is called at the same time with `Serve`, `Close`
	// may finish first, and `Serve` may still block.
	// See https://github.com/golang/go/issues/20239.
	// Here we set a 2 second timeout for the stream server wait,
	// if it timeout, an error log is generated.
	// TODO(random-liu): Get rid of this after https://github.com/golang/go/issues/20239
	// is fixed.
	const streamServerStopTimeout = 2 * time.Second
	select {
	case err := <-streamServerErrCh:
		if err != nil {
			streamServerErr = err
		}
		logrus.Info("Stream server stopped")
	case <-time.After(streamServerStopTimeout):
		logrus.Errorf("Stream server is not stopped in %q", streamServerStopTimeout)
	}
	if eventMonitorErr != nil {
		return errors.Wrap(eventMonitorErr, "event monitor error")
	}
	if streamServerErr != nil {
		return errors.Wrap(streamServerErr, "stream server error")
	}
	if cniNetConfMonitorErr != nil {
		return errors.Wrap(cniNetConfMonitorErr, "cni network conf monitor error")
	}
	return nil
}

// Close stops the CRI service.
// TODO(random-liu): Make close synchronous.
func (c *criService) Close() error {
	logrus.Info("Stop CRI service")
	if err := c.cniNetConfMonitor.stop(); err != nil {
		logrus.WithError(err).Error("failed to stop cni network conf monitor")
	}
	c.eventMonitor.stop()
	if err := c.streamServer.Stop(); err != nil {
		return errors.Wrap(err, "failed to stop stream server")
	}
	return nil
}

// imageFSPath returns containerd image filesystem path.
// Note that if containerd changes directory layout, we also needs to change this.
func imageFSPath(rootDir, snapshotter string) string {
	return filepath.Join(rootDir, fmt.Sprintf("%s.%s", plugin.SnapshotPlugin, snapshotter))
}

func loadOCISpec(filename string) (*oci.Spec, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to open base OCI spec: %s", filename)
	}
	defer file.Close()

	spec := oci.Spec{}
	if err := json.NewDecoder(file).Decode(&spec); err != nil {
		return nil, errors.Wrap(err, "failed to parse base OCI spec file")
	}

	return &spec, nil
}

func loadBaseOCISpecs(config *criconfig.Config) (map[string]*oci.Spec, error) {
	specs := map[string]*oci.Spec{}
	for _, cfg := range config.Runtimes {
		if cfg.BaseRuntimeSpec == "" {
			continue
		}

		// Don't load same file twice
		if _, ok := specs[cfg.BaseRuntimeSpec]; ok {
			continue
		}

		spec, err := loadOCISpec(cfg.BaseRuntimeSpec)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to load base OCI spec from file: %s", cfg.BaseRuntimeSpec)
		}

		specs[cfg.BaseRuntimeSpec] = spec
	}

	return specs, nil
}
