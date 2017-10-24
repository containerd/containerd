//+build windows

package windows

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/Microsoft/hcsshim"
	"github.com/Microsoft/opengcs/client"
	"github.com/containerd/containerd/errdefs"
	specs "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/pkg/errors"
)

// newContainerConfig generates a hcsshim container configuration from the
// provided OCI Spec
func newContainerConfig(ctx context.Context, owner, id string, spec *specs.Spec) (*hcsshim.ContainerConfig, error) {
	var (
		conf = &hcsshim.ContainerConfig{
			SystemType: "Container",
			Name:       id,
			Owner:      owner,
			HostName:   spec.Hostname,
		}
	)

	if spec.Windows.Network != nil {
		conf.AllowUnqualifiedDNSQuery = spec.Windows.Network.AllowUnqualifiedDNSQuery
		conf.EndpointList = spec.Windows.Network.EndpointList
		conf.NetworkSharedContainerName = spec.Windows.Network.NetworkSharedContainerName
		if spec.Windows.Network.DNSSearchList != nil {
			conf.DNSSearchList = strings.Join(spec.Windows.Network.DNSSearchList, ",")
		}
	}

	return conf, nil
}

// newWindowsContainerConfig generates a hcsshim Windows container
// configuration from the provided OCI Spec
func newWindowsContainerConfig(ctx context.Context, owner, id string, spec *specs.Spec) (*hcsshim.ContainerConfig, error) {
	conf, err := newContainerConfig(ctx, owner, id, spec)
	if err != nil {
		return nil, err
	}
	conf.IgnoreFlushesDuringBoot = spec.Windows.IgnoreFlushesDuringBoot

	if len(spec.Windows.LayerFolders) < 2 {
		return nil, errors.Wrap(errdefs.ErrInvalidArgument,
			"spec.Windows.LayerFolders must have at least 2 layers")
	}
	var (
		layerFolderPath = spec.Windows.LayerFolders[0]
		layerFolders    = spec.Windows.LayerFolders[1:]
		layerID         = filepath.Base(layerFolderPath)
	)

	// TODO: use the create request Mount for those
	for _, layerPath := range layerFolders {
		_, filename := filepath.Split(layerPath)
		guid, err := hcsshim.NameToGuid(filename)
		if err != nil {
			return nil, errors.Wrapf(err, "unable to get GUID for %s", filename)
		}
		conf.Layers = append(conf.Layers, hcsshim.Layer{
			ID:   guid.ToString(),
			Path: layerPath,
		})
	}
	conf.LayerFolderPath = layerFolderPath

	var di = hcsshim.DriverInfo{
		Flavour: 1, // filter driver
		HomeDir: filepath.Dir(layerFolderPath),
	}

	conf.VolumePath, err = hcsshim.GetLayerMountPath(di, layerID)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to getmount path for layer %s: driverInfo: %#v", layerID, di)
	}

	if spec.Windows.HyperV != nil {
		conf.HvPartition = true
		for _, layerPath := range layerFolders {
			utilityVMPath := spec.Windows.HyperV.UtilityVMPath
			_, err := os.Stat(utilityVMPath)
			if err == nil {
				conf.HvRuntime = &hcsshim.HvRuntime{ImagePath: utilityVMPath}
				break
			} else if !os.IsNotExist(err) {
				return nil, errors.Wrapf(err, "failed to access layer %s", layerPath)
			}
		}
	}

	if spec.Windows.CredentialSpec != nil {
		conf.Credentials = spec.Windows.CredentialSpec.(string)
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
		conf.MappedDirectories = mds
	}

	return conf, nil
}

// newLinuxConfig generates a hcsshim Linux container configuration from the
// provided OCI Spec
func newLinuxConfig(ctx context.Context, owner, id string, spec *specs.Spec) (*hcsshim.ContainerConfig, error) {
	conf, err := newContainerConfig(ctx, owner, id, spec)
	if err != nil {
		return nil, err
	}

	conf.ContainerType = "Linux"
	conf.HvPartition = true

	if len(spec.Windows.LayerFolders) < 1 {
		return nil, errors.Wrap(errdefs.ErrInvalidArgument,
			"spec.Windows.LayerFolders must have at least 1 layer")
	}
	var (
		layerFolders = spec.Windows.LayerFolders
	)

	config := &client.Config{}
	if err := config.GenerateDefault(nil); err != nil {
		return nil, err
	}

	conf.HvRuntime = &hcsshim.HvRuntime{
		ImagePath:           config.KirdPath,
		LinuxKernelFile:     config.KernelFile,
		LinuxInitrdFile:     config.InitrdFile,
		LinuxBootParameters: config.BootParameters,
	}

	// TODO: use the create request Mount for those
	for _, layerPath := range layerFolders {
		_, filename := filepath.Split(layerPath)
		guid, err := hcsshim.NameToGuid(filename)
		if err != nil {
			return nil, errors.Wrapf(err, "unable to get GUID for %s", filename)
		}
		conf.Layers = append(conf.Layers, hcsshim.Layer{
			ID:   guid.ToString(),
			Path: filepath.Join(layerPath, "layer.vhd"),
		})
	}

	return conf, nil
}

func newProcessConfig(processSpec *specs.Process, pset *pipeSet) *hcsshim.ProcessConfig {
	conf := &hcsshim.ProcessConfig{
		EmulateConsole:   pset.src.Terminal,
		CreateStdInPipe:  pset.stdin != nil,
		CreateStdOutPipe: pset.stdout != nil,
		CreateStdErrPipe: pset.stderr != nil,
		User:             processSpec.User.Username,
		Environment:      make(map[string]string),
		WorkingDirectory: processSpec.Cwd,
	}

	if processSpec.ConsoleSize != nil {
		conf.ConsoleSize = [2]uint{processSpec.ConsoleSize.Height, processSpec.ConsoleSize.Width}
	}

	// Convert OCI Env format to HCS's
	for _, s := range processSpec.Env {
		arr := strings.SplitN(s, "=", 2)
		if len(arr) == 2 {
			conf.Environment[arr[0]] = arr[1]
		}
	}

	return conf
}

func newWindowsProcessConfig(processSpec *specs.Process, pset *pipeSet) *hcsshim.ProcessConfig {
	conf := newProcessConfig(processSpec, pset)
	conf.CommandLine = strings.Join(processSpec.Args, " ")
	return conf
}

func newLinuxProcessConfig(processSpec *specs.Process, pset *pipeSet) (*hcsshim.ProcessConfig, error) {
	conf := newProcessConfig(processSpec, pset)
	conf.CommandArgs = processSpec.Args
	return conf, nil
}
