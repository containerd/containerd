// +build windows

package hcs

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/Microsoft/hcsshim"
	"github.com/Sirupsen/logrus"
	"github.com/containerd/containerd"
	"github.com/containerd/containerd/log"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/pkg/errors"
)

const (
	layerFile               = "layer"
	defaultTerminateTimeout = 5 * time.Minute
)

func LoadAll(ctx context.Context, owner, rootDir string) (map[string]*HCS, error) {
	ctrProps, err := hcsshim.GetContainers(hcsshim.ComputeSystemQuery{})
	if err != nil {
		return nil, errors.Wrap(err, "failed to retrieve running containers")
	}

	containers := make(map[string]*HCS)
	for _, p := range ctrProps {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		if p.Owner != owner || p.SystemType != "Container" {
			continue
		}

		// TODO: take context in account
		container, err := hcsshim.OpenContainer(p.ID)
		if err != nil {
			return nil, errors.Wrapf(err, "failed open container %s", p.ID)
		}
		stateDir := filepath.Join(rootDir, p.ID)
		b, err := ioutil.ReadFile(filepath.Join(stateDir, layerFile))
		containers[p.ID] = &HCS{
			id:              p.ID,
			container:       container,
			stateDir:        stateDir,
			layerFolderPath: string(b),
			conf: Configuration{
				TerminateDuration: defaultTerminateTimeout,
			},
		}
	}

	return containers, nil
}

// New creates a new container (but doesn't start) it.
func New(rootDir, owner, containerID string, spec specs.Spec, conf Configuration, cio containerd.IO) (*HCS, error) {
	stateDir := filepath.Join(rootDir, containerID)
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		return nil, errors.Wrapf(err, "unable to create container state dir %s", stateDir)
	}

	if conf.TerminateDuration == 0 {
		conf.TerminateDuration = defaultTerminateTimeout
	}

	h := &HCS{
		stateDir: stateDir,
		owner:    owner,
		id:       containerID,
		spec:     spec,
		conf:     conf,
	}

	sio, err := newSIO(cio)
	if err != nil {
		return nil, err
	}
	h.io = sio
	runtime.SetFinalizer(sio, func(s *shimIO) {
		s.Close()
	})

	hcsConf, err := h.newHCSConfiguration()
	if err != nil {
		return nil, err
	}

	ctr, err := hcsshim.CreateContainer(containerID, hcsConf)
	if err != nil {
		removeLayer(context.TODO(), hcsConf.LayerFolderPath)
		return nil, err
	}
	h.container = ctr
	h.layerFolderPath = hcsConf.LayerFolderPath

	return h, nil
}

type HCS struct {
	stateDir        string
	owner           string
	id              string
	spec            specs.Spec
	conf            Configuration
	io              *shimIO
	container       hcsshim.Container
	initProcess     hcsshim.Process
	layerFolderPath string
}

// Start starts the associated container and instantiate the init
// process within it.
func (s *HCS) Start(ctx context.Context, servicing bool) error {
	if s.initProcess != nil {
		return errors.New("init process already started")
	}
	if err := s.container.Start(); err != nil {
		if err := s.Terminate(ctx); err != nil {
			log.G(ctx).WithError(err).Errorf("failed to terminate container %s", s.id)
		}
		return err
	}

	proc, err := s.newProcess(ctx, s.io, s.spec.Process)
	if err != nil {
		s.Terminate(ctx)
		return err
	}

	s.initProcess = proc

	return nil
}

// Pid returns the pid of the container init process
func (s *HCS) Pid() int {
	return s.initProcess.Pid()
}

// ExitCode waits for the container to exit and return the exit code
// of the init process
func (s *HCS) ExitCode(ctx context.Context) (uint32, error) {
	// TODO: handle a context cancellation
	if err := s.initProcess.Wait(); err != nil {
		if herr, ok := err.(*hcsshim.ProcessError); ok && herr.Err != syscall.ERROR_BROKEN_PIPE {
			return 255, errors.Wrapf(err, "failed to wait for container '%s' init process", s.id)
		}
		// container is probably dead, let's try to get its exit code
	}

	ec, err := s.initProcess.ExitCode()
	if err != nil {
		if herr, ok := err.(*hcsshim.ProcessError); ok && herr.Err != syscall.ERROR_BROKEN_PIPE {
			return 255, errors.Wrapf(err, "failed to get container '%s' init process exit code", s.id)
		}
		// Well, unknown exit code it is
		ec = 255
	}

	return uint32(ec), err
}

// Exec starts a new process within the container
func (s *HCS) Exec(ctx context.Context, procSpec specs.Process, io containerd.IO) (*Process, error) {
	sio, err := newSIO(io)
	if err != nil {
		return nil, err
	}
	p, err := s.newProcess(ctx, sio, procSpec)
	if err != nil {
		return nil, err
	}

	return &Process{
		containerID: s.id,
		p:           p,
		status:      containerd.RunningStatus,
	}, nil
}

// newProcess create a new process within a running container. This is
// used to create both the init process and subsequent 'exec'
// processes.
func (s *HCS) newProcess(ctx context.Context, sio *shimIO, procSpec specs.Process) (hcsshim.Process, error) {
	conf := hcsshim.ProcessConfig{
		EmulateConsole:   sio.terminal,
		CreateStdInPipe:  sio.stdin != nil,
		CreateStdOutPipe: sio.stdout != nil,
		CreateStdErrPipe: sio.stderr != nil,
		User:             procSpec.User.Username,
		CommandLine:      strings.Join(procSpec.Args, " "),
		Environment:      ociSpecEnvToHCSEnv(procSpec.Env),
		WorkingDirectory: procSpec.Cwd,
	}
	conf.ConsoleSize[0] = procSpec.ConsoleSize.Height
	conf.ConsoleSize[1] = procSpec.ConsoleSize.Width

	if conf.WorkingDirectory == "" {
		conf.WorkingDirectory = s.spec.Process.Cwd
	}

	proc, err := s.container.CreateProcess(&conf)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to create process with conf %#v", conf)

	}
	pid := proc.Pid()

	stdin, stdout, stderr, err := proc.Stdio()
	if err != nil {
		s.Terminate(ctx)
		return nil, err
	}

	if sio.stdin != nil {
		go func() {
			log.G(ctx).WithField("pid", pid).Debug("stdin: copy started")
			io.Copy(stdin, sio.stdin)
			log.G(ctx).WithField("pid", pid).Debug("stdin: copy done")
			stdin.Close()
			sio.stdin.Close()
		}()
	} else {
		proc.CloseStdin()
	}

	if sio.stdout != nil {
		go func() {
			log.G(ctx).WithField("pid", pid).Debug("stdout: copy started")
			io.Copy(sio.stdout, stdout)
			log.G(ctx).WithField("pid", pid).Debug("stdout: copy done")
			stdout.Close()
			sio.stdout.Close()
		}()
	}

	if sio.stderr != nil {
		go func() {
			log.G(ctx).WithField("pid", pid).Debug("stderr: copy started")
			io.Copy(sio.stderr, stderr)
			log.G(ctx).WithField("pid", pid).Debug("stderr: copy done")
			stderr.Close()
			sio.stderr.Close()
		}()
	}

	return proc, nil
}

// Terminate stop a running container.
func (s *HCS) Terminate(ctx context.Context) error {
	err := s.container.Terminate()
	switch {
	case hcsshim.IsPending(err):
		// TODO: take the context into account
		err = s.container.WaitTimeout(s.conf.TerminateDuration)
	case hcsshim.IsAlreadyStopped(err):
		err = nil
	}

	return err
}

func (s *HCS) Shutdown(ctx context.Context) error {
	err := s.container.Shutdown()
	switch {
	case hcsshim.IsPending(err):
		// TODO: take the context into account
		err = s.container.WaitTimeout(s.conf.TerminateDuration)
	case hcsshim.IsAlreadyStopped(err):
		err = nil
	}

	if err != nil {
		log.G(ctx).WithError(err).Debugf("failed to shutdown container %s, calling terminate", s.id)
		return s.Terminate(ctx)
	}

	return nil
}

// Remove start a servicing container if needed then cleanup the container
// resources
func (s *HCS) Remove(ctx context.Context) error {
	defer func() {
		if err := s.Shutdown(ctx); err != nil {
			log.G(ctx).WithError(err).WithField("id", s.id).
				Errorf("failed to shutdown/terminate container")
		}

		if s.initProcess != nil {
			if err := s.initProcess.Close(); err != nil {
				log.G(ctx).WithError(err).WithFields(logrus.Fields{"pid": s.Pid(), "id": s.id}).
					Errorf("failed to clean init process resources")
			}
		}
		if err := s.container.Close(); err != nil {
			log.G(ctx).WithError(err).WithField("id", s.id).Errorf("failed to clean container resources")
		}

		// Cleanup folder layer
		if err := removeLayer(ctx, s.layerFolderPath); err == nil {
			os.RemoveAll(s.stateDir)
		}
	}()

	if update, err := s.container.HasPendingUpdates(); err != nil || !update {
		return nil
	}

	// TODO: take the context into account
	serviceHCS, err := New(s.stateDir, s.owner, s.id+"_servicing", s.spec, s.conf, containerd.IO{})
	if err != nil {
		log.G(ctx).WithError(err).WithField("id", s.id).Warn("could not create servicing container")
		return nil
	}
	defer serviceHCS.container.Close()

	err = serviceHCS.Start(ctx, true)
	if err != nil {
		if err := serviceHCS.Terminate(ctx); err != nil {
			log.G(ctx).WithError(err).WithField("id", s.id).Errorf("failed to terminate servicing container for %s")
		}
		log.G(ctx).WithError(err).WithField("id", s.id).Errorf("failed to start servicing container")
		return nil
	}

	// wait for the container to exit
	_, err = serviceHCS.ExitCode(ctx)
	if err != nil {
		if err := serviceHCS.Terminate(ctx); err != nil {
			log.G(ctx).WithError(err).WithField("id", s.id).Errorf("failed to terminate servicing container for %s")
		}
		log.G(ctx).WithError(err).WithField("id", s.id).Errorf("failed to get servicing container exit code")
	}

	serviceHCS.container.WaitTimeout(s.conf.TerminateDuration)

	return nil
}

// newHCSConfiguration generates a hcsshim configuration from the instance
// OCI Spec and hcs.Configuration.
func (s *HCS) newHCSConfiguration() (*hcsshim.ContainerConfig, error) {
	configuration := &hcsshim.ContainerConfig{
		SystemType:                 "Container",
		Name:                       s.id,
		Owner:                      s.owner,
		HostName:                   s.spec.Hostname,
		IgnoreFlushesDuringBoot:    s.conf.IgnoreFlushesDuringBoot,
		HvPartition:                s.conf.UseHyperV,
		AllowUnqualifiedDNSQuery:   s.conf.AllowUnqualifiedDNSQuery,
		EndpointList:               s.conf.NetworkEndpoints,
		NetworkSharedContainerName: s.conf.NetworkSharedContainerID,
		Credentials:                s.conf.Credentials,
	}

	// TODO: use the create request Mount for those
	for _, layerPath := range s.conf.Layers {
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

	if len(s.spec.Mounts) > 0 {
		mds := make([]hcsshim.MappedDir, len(s.spec.Mounts))
		for i, mount := range s.spec.Mounts {
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

	if s.conf.DNSSearchList != nil {
		configuration.DNSSearchList = strings.Join(s.conf.DNSSearchList, ",")
	}

	if configuration.HvPartition {
		for _, layerPath := range s.conf.Layers {
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
		di.HomeDir = filepath.Dir(s.conf.Layers[0])
	}

	// Windows doesn't support creating a container with a readonly
	// filesystem, so always create a RW one
	if err := hcsshim.CreateSandboxLayer(di, s.id, s.conf.Layers[0], s.conf.Layers); err != nil {
		return nil, errors.Wrapf(err, "failed to create sandbox layer for %s: layers: %#v, driverInfo: %#v",
			s.id, configuration.Layers, di)
	}

	configuration.LayerFolderPath = filepath.Join(di.HomeDir, s.id)
	if err := ioutil.WriteFile(filepath.Join(s.stateDir, layerFile), []byte(configuration.LayerFolderPath), 0644); err != nil {
		log.L.WithError(err).Warnf("failed to save active layer %s", configuration.LayerFolderPath)
	}

	err := hcsshim.ActivateLayer(di, s.id)
	if err != nil {
		removeLayer(context.TODO(), configuration.LayerFolderPath)
		return nil, errors.Wrapf(err, "failed to active layer %s", configuration.LayerFolderPath)
	}

	err = hcsshim.PrepareLayer(di, s.id, s.conf.Layers)
	if err != nil {
		removeLayer(context.TODO(), configuration.LayerFolderPath)
		return nil, errors.Wrapf(err, "failed to prepare layer %s", configuration.LayerFolderPath)
	}

	volumePath, err := hcsshim.GetLayerMountPath(di, s.id)
	if err != nil {
		if err := hcsshim.DestroyLayer(di, s.id); err != nil {
			log.L.Warnf("failed to DestroyLayer %s: %s", s.id, err)
		}
		return nil, errors.Wrapf(err, "failed to getmount path for layer %s: driverInfo: %#v", s.id, di)
	}
	configuration.VolumePath = volumePath

	f, err := os.OpenFile(fmt.Sprintf("%s-hcs.json", s.id), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 066)
	if err != nil {
		fmt.Println("failed to create file:", err)
	} else {
		defer f.Close()
		enc := json.NewEncoder(f)
		enc.Encode(configuration)
	}

	return configuration, nil
}

// removeLayer delete the given layer, all associated containers must have
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
