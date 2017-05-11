// +build windows

package hcs

import (
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Microsoft/hcsshim"
	"github.com/Sirupsen/logrus"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/plugin"
	"github.com/containerd/containerd/windows/pid"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/pkg/errors"
)

const (
	layerFile               = "layer"
	defaultTerminateTimeout = 5 * time.Minute
)

func (s *HCS) LoadContainers(ctx context.Context) ([]*Container, error) {
	ctrProps, err := hcsshim.GetContainers(hcsshim.ComputeSystemQuery{})
	if err != nil {
		return nil, errors.Wrap(err, "failed to retrieve running containers")
	}

	containers := make([]*Container, 0)
	for _, p := range ctrProps {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		if p.Owner != s.owner || p.SystemType != "Container" {
			continue
		}

		container, err := hcsshim.OpenContainer(p.ID)
		if err != nil {
			return nil, errors.Wrapf(err, "failed open container %s", p.ID)
		}
		stateDir := filepath.Join(s.stateDir, p.ID)
		b, err := ioutil.ReadFile(filepath.Join(stateDir, layerFile))
		containers = append(containers, &Container{
			id:              p.ID,
			Container:       container,
			stateDir:        stateDir,
			hcs:             s,
			io:              &IO{},
			layerFolderPath: string(b),
			conf: Configuration{
				TerminateDuration: defaultTerminateTimeout,
			},
		})
	}

	return containers, nil
}

func New(owner, rootDir string) *HCS {
	return &HCS{
		stateDir: rootDir,
		owner:    owner,
		pidPool:  pid.NewPool(),
	}
}

type HCS struct {
	stateDir string
	owner    string
	pidPool  *pid.Pool
}

func (s *HCS) CreateContainer(ctx context.Context, id string, spec specs.Spec, conf Configuration, io *IO) (c *Container, err error) {
	pid, err := s.pidPool.Get()
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			s.pidPool.Put(pid)
		}
	}()

	stateDir := filepath.Join(s.stateDir, id)
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		return nil, errors.Wrapf(err, "unable to create container state dir %s", stateDir)
	}
	defer func() {
		if err != nil {
			os.RemoveAll(stateDir)
		}
	}()

	if conf.TerminateDuration == 0 {
		conf.TerminateDuration = defaultTerminateTimeout
	}

	ctrConf, err := newContainerConfig(s.owner, id, spec, conf)
	if err != nil {
		return nil, err
	}

	layerPathFile := filepath.Join(stateDir, layerFile)
	if err := ioutil.WriteFile(layerPathFile, []byte(ctrConf.LayerFolderPath), 0644); err != nil {
		log.G(ctx).WithError(err).Warnf("failed to save active layer %s", ctrConf.LayerFolderPath)
	}

	ctr, err := hcsshim.CreateContainer(id, ctrConf)
	if err != nil {
		removeLayer(ctx, ctrConf.LayerFolderPath)
		return nil, errors.Wrapf(err, "failed to create container %s", id)
	}

	err = ctr.Start()
	if err != nil {
		ctr.Terminate()
		removeLayer(ctx, ctrConf.LayerFolderPath)
		return nil, errors.Wrapf(err, "failed to start container %s", id)
	}

	return &Container{
		Container:       ctr,
		id:              id,
		pid:             pid,
		spec:            spec,
		conf:            conf,
		stateDir:        stateDir,
		io:              io,
		hcs:             s,
		layerFolderPath: ctrConf.LayerFolderPath,
		processes:       make([]*Process, 0),
	}, nil
}

type Container struct {
	sync.Mutex
	hcsshim.Container

	id              string
	stateDir        string
	pid             uint32
	spec            specs.Spec
	conf            Configuration
	io              *IO
	hcs             *HCS
	layerFolderPath string

	processes []*Process
}

func (c *Container) ID() string {
	return c.id
}

func (c *Container) Pid() uint32 {
	return c.pid
}

func (c *Container) Processes() []*Process {
	return c.processes
}

func (c *Container) Start(ctx context.Context) error {
	_, err := c.addProcess(ctx, c.spec.Process, c.io)
	return err
}

func (c *Container) getDeathErr(err error) error {
	switch {
	case hcsshim.IsPending(err):
		err = c.WaitTimeout(c.conf.TerminateDuration)
	case hcsshim.IsAlreadyStopped(err):
		err = nil
	}
	return err
}

func (c *Container) Kill(ctx context.Context) error {
	return c.getDeathErr(c.Terminate())
}

func (c *Container) Stop(ctx context.Context) error {
	err := c.getDeathErr(c.Shutdown())
	if err != nil {
		log.G(ctx).WithError(err).Debugf("failed to shutdown container %s, calling terminate", c.id)
		return c.getDeathErr(c.Terminate())
	}
	return nil
}

func (c *Container) CloseStdin(ctx context.Context, pid uint32) error {
	var proc *Process
	c.Lock()
	for _, p := range c.processes {
		if p.Pid() == pid {
			proc = p
			break
		}
	}
	c.Unlock()
	if proc == nil {
		return errors.Errorf("no such process %v", pid)
	}

	return proc.CloseStdin()
}

func (c *Container) Pty(ctx context.Context, pid uint32, size plugin.ConsoleSize) error {
	var proc *Process
	c.Lock()
	for _, p := range c.processes {
		if p.Pid() == pid {
			proc = p
			break
		}
	}
	c.Unlock()
	if proc == nil {
		return errors.Errorf("no such process %v", pid)
	}

	return proc.ResizeConsole(uint16(size.Width), uint16(size.Height))
}

func (c *Container) Delete(ctx context.Context) {
	defer func() {
		if err := c.Stop(ctx); err != nil {
			log.G(ctx).WithError(err).WithField("id", c.id).
				Errorf("failed to shutdown/terminate container")
		}

		c.Lock()
		for _, p := range c.processes {
			if err := p.Delete(); err != nil {
				log.G(ctx).WithError(err).WithFields(logrus.Fields{"pid": p.Pid(), "id": c.id}).
					Errorf("failed to clean process resources")
			}
		}
		c.Unlock()

		if err := c.Close(); err != nil {
			log.G(ctx).WithError(err).WithField("id", c.id).Errorf("failed to clean container resources")
		}

		c.io.Close()

		// Cleanup folder layer
		if err := removeLayer(ctx, c.layerFolderPath); err == nil {
			os.RemoveAll(c.stateDir)
		}
	}()

	if update, err := c.HasPendingUpdates(); err != nil || !update {
		return
	}

	serviceCtr, err := c.hcs.CreateContainer(ctx, c.id+"_servicing", c.spec, c.conf, &IO{})
	if err != nil {
		log.G(ctx).WithError(err).WithField("id", c.id).Warn("could not create servicing container")
		return
	}
	defer serviceCtr.Close()

	err = serviceCtr.Start(ctx)
	if err != nil {
		log.G(ctx).WithError(err).WithField("id", c.id).Warn("failed to start servicing container")
		serviceCtr.Terminate()
		return
	}

	err = serviceCtr.processes[0].Wait()
	if err == nil {
		_, err = serviceCtr.processes[0].ExitCode()
		log.G(ctx).WithError(err).WithField("id", c.id).Errorf("failed to retrieve servicing container exit code")
	}

	if err != nil {
		if err := serviceCtr.Terminate(); err != nil {
			log.G(ctx).WithError(err).WithField("id", c.id).Errorf("failed to terminate servicing container")
		}
	}
}

func (c *Container) ExitCode() (uint32, error) {
	if len(c.processes) == 0 {
		return 255, errors.New("container not started")
	}
	return c.processes[0].ExitCode()
}

func (c *Container) GetConfiguration() Configuration {
	return c.conf
}

func (c *Container) AddProcess(ctx context.Context, spec specs.Process, io *IO) (*Process, error) {
	if len(c.processes) == 0 {
		return nil, errors.New("container not started")
	}

	return c.addProcess(ctx, spec, io)
}

func (c *Container) addProcess(ctx context.Context, spec specs.Process, pio *IO) (*Process, error) {
	// If we don't have a process yet, reused the container pid
	var pid uint32
	if len(c.processes) == 0 {
		pid = c.pid
	} else {
		pid, err := c.hcs.pidPool.Get()
		if err != nil {
			return nil, err
		}
		defer func() {
			if err != nil {
				c.hcs.pidPool.Put(pid)
			}
		}()
	}

	conf := hcsshim.ProcessConfig{
		EmulateConsole:   pio.terminal,
		CreateStdInPipe:  pio.stdin != nil,
		CreateStdOutPipe: pio.stdout != nil,
		CreateStdErrPipe: pio.stderr != nil,
		User:             spec.User.Username,
		CommandLine:      strings.Join(spec.Args, " "),
		Environment:      ociSpecEnvToHCSEnv(spec.Env),
		WorkingDirectory: spec.Cwd,
		ConsoleSize:      [2]uint{spec.ConsoleSize.Height, spec.ConsoleSize.Width},
	}

	if conf.WorkingDirectory == "" {
		conf.WorkingDirectory = c.spec.Process.Cwd
	}

	proc, err := c.CreateProcess(&conf)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to create process")
	}

	stdin, stdout, stderr, err := proc.Stdio()
	if err != nil {
		proc.Kill()
		return nil, errors.Wrapf(err, "failed to retrieve process stdio")
	}

	if pio.stdin != nil {
		go func() {
			log.G(ctx).WithFields(logrus.Fields{"id": c.id, "pid": pid}).Debug("stdin: copy started")
			io.Copy(stdin, pio.stdin)
			log.G(ctx).WithFields(logrus.Fields{"id": c.id, "pid": pid}).Debug("stdin: copy done")
			stdin.Close()
			pio.stdin.Close()
		}()
	} else {
		proc.CloseStdin()
	}

	if pio.stdout != nil {
		go func() {
			log.G(ctx).WithFields(logrus.Fields{"id": c.id, "pid": pid}).Debug("stdout: copy started")
			io.Copy(pio.stdout, stdout)
			log.G(ctx).WithFields(logrus.Fields{"id": c.id, "pid": pid}).Debug("stdout: copy done")
			stdout.Close()
			pio.stdout.Close()
		}()
	}

	if pio.stderr != nil {
		go func() {
			log.G(ctx).WithFields(logrus.Fields{"id": c.id, "pid": pid}).Debug("stderr: copy started")
			io.Copy(pio.stderr, stderr)
			log.G(ctx).WithFields(logrus.Fields{"id": c.id, "pid": pid}).Debug("stderr: copy done")
			stderr.Close()
			pio.stderr.Close()
		}()
	}

	p := &Process{
		Process: proc,
		pid:     pid,
		io:      pio,
		ecSync:  make(chan struct{}),
	}

	c.Lock()
	c.processes = append(c.processes, p)
	idx := len(c.processes) - 1
	c.Unlock()

	go func() {
		p.ec, p.ecErr = processExitCode(c.ID(), p)
		close(p.ecSync)
		c.Lock()
		p.Delete()
		// Remove process from slice (but keep the init one around)
		if idx > 0 {
			c.processes[idx] = c.processes[len(c.processes)-1]
			c.processes[len(c.processes)-1] = nil
			c.processes = c.processes[:len(c.processes)-1]
		}
		c.Unlock()
	}()

	return p, nil
}

// newHCSConfiguration generates a hcsshim configuration from the instance
// OCI Spec and hcs.Configuration.
func newContainerConfig(owner, id string, spec specs.Spec, conf Configuration) (*hcsshim.ContainerConfig, error) {
	configuration := &hcsshim.ContainerConfig{
		SystemType:                 "Container",
		Name:                       id,
		Owner:                      owner,
		HostName:                   spec.Hostname,
		IgnoreFlushesDuringBoot:    conf.IgnoreFlushesDuringBoot,
		HvPartition:                conf.UseHyperV,
		AllowUnqualifiedDNSQuery:   conf.AllowUnqualifiedDNSQuery,
		EndpointList:               conf.NetworkEndpoints,
		NetworkSharedContainerName: conf.NetworkSharedContainerID,
		Credentials:                conf.Credentials,
	}

	// TODO: use the create request Mount for those
	for _, layerPath := range conf.Layers {
		_, filename := filepath.Split(layerPath)
		guid, err := hcsshim.NameToGuid(filename)
		if err != nil {
			return nil, err
		}
		configuration.Layers = append(configuration.Layers, hcsshim.Layer{
			ID:   guid.ToString(),
			Path: layerPath,
		})
	}

	if len(spec.Mounts) > 0 {
		mds := make([]hcsshim.MappedDir, len(spec.Mounts))
		for i, mount := range spec.Mounts {
			mds[i] = hcsshim.MappedDir{
				HostPath:      mount.Source,
				ContainerPath: mount.Destination,
				ReadOnly:      false,
			}
			for _, o := range mount.Options {
				if strings.ToLower(o) == "ro" {
					mds[i].ReadOnly = true
				}
			}
		}
		configuration.MappedDirectories = mds
	}

	if conf.DNSSearchList != nil {
		configuration.DNSSearchList = strings.Join(conf.DNSSearchList, ",")
	}

	if configuration.HvPartition {
		for _, layerPath := range conf.Layers {
			utilityVMPath := filepath.Join(layerPath, "UtilityVM")
			_, err := os.Stat(utilityVMPath)
			if err == nil {
				configuration.HvRuntime = &hcsshim.HvRuntime{ImagePath: utilityVMPath}
				break
			} else if !os.IsNotExist(err) {
				return nil, errors.Wrapf(err, "failed to access layer %s", layerPath)
			}
		}
	}

	if len(configuration.Layers) == 0 {
		// TODO: support starting with 0 layers, this mean we need the "filter" directory as parameter
		return nil, errors.New("at least one layers must be provided")
	}

	di := hcsshim.DriverInfo{
		Flavour: 1, // filter driver
	}

	if len(configuration.Layers) > 0 {
		di.HomeDir = filepath.Dir(conf.Layers[0])
	}

	// Windows doesn't support creating a container with a readonly
	// filesystem, so always create a RW one
	if err := hcsshim.CreateSandboxLayer(di, id, conf.Layers[0], conf.Layers); err != nil {
		return nil, errors.Wrapf(err, "failed to create sandbox layer for %s: layers: %#v, driverInfo: %#v",
			id, configuration.Layers, di)
	}
	configuration.LayerFolderPath = filepath.Join(di.HomeDir, id)

	err := hcsshim.ActivateLayer(di, id)
	if err != nil {
		removeLayer(context.TODO(), configuration.LayerFolderPath)
		return nil, errors.Wrapf(err, "failed to active layer %s", configuration.LayerFolderPath)
	}

	err = hcsshim.PrepareLayer(di, id, conf.Layers)
	if err != nil {
		removeLayer(context.TODO(), configuration.LayerFolderPath)
		return nil, errors.Wrapf(err, "failed to prepare layer %s", configuration.LayerFolderPath)
	}

	volumePath, err := hcsshim.GetLayerMountPath(di, id)
	if err != nil {
		if err := hcsshim.DestroyLayer(di, id); err != nil {
			log.L.Warnf("failed to DestroyLayer %s: %s", id, err)
		}
		return nil, errors.Wrapf(err, "failed to getmount path for layer %s: driverInfo: %#v", id, di)
	}
	configuration.VolumePath = volumePath

	return configuration, nil
}

// removeLayer deletes the given layer, all associated containers must have
// been shutdown for this to succeed.
func removeLayer(ctx context.Context, path string) error {
	layerID := filepath.Base(path)
	parentPath := filepath.Dir(path)
	di := hcsshim.DriverInfo{
		Flavour: 1, // filter driver
		HomeDir: parentPath,
	}

	err := hcsshim.UnprepareLayer(di, layerID)
	if err != nil {
		log.G(ctx).WithError(err).Warnf("failed to unprepare layer %s for removal", path)
	}

	err = hcsshim.DeactivateLayer(di, layerID)
	if err != nil {
		log.G(ctx).WithError(err).Warnf("failed to deactivate layer %s for removal", path)
	}

	removePath := filepath.Join(parentPath, fmt.Sprintf("%s-removing", layerID))
	err = os.Rename(path, removePath)
	if err != nil {
		log.G(ctx).WithError(err).Warnf("failed to rename container layer %s for removal", path)
		removePath = path
	}
	if err := hcsshim.DestroyLayer(di, removePath); err != nil {
		log.G(ctx).WithError(err).Errorf("failed to remove container layer %s", removePath)
		return err
	}

	return nil
}

// ociSpecEnvToHCSEnv converts from the OCI Spec ENV format to the one
// expected by HCS.
func ociSpecEnvToHCSEnv(a []string) map[string]string {
	env := make(map[string]string)
	for _, s := range a {
		arr := strings.SplitN(s, "=", 2)
		if len(arr) == 2 {
			env[arr[0]] = arr[1]
		}
	}
	return env
}
