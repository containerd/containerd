//+build windows

package windows

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Microsoft/hcsshim"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/log"
	specs "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/pkg/errors"
)

// newContainerConfig generates a hcsshim container configuration from the
// provided OCI Spec
func newContainerConfig(ctx context.Context, owner, id string, spec *specs.Spec) (*hcsshim.ContainerConfig, error) {
	if len(spec.Windows.LayerFolders) == 0 {
		return nil, errors.Wrap(errdefs.ErrInvalidArgument,
			"spec.Windows.LayerFolders cannot be empty")
	}

	var (
		layerFolders = spec.Windows.LayerFolders
		conf         = &hcsshim.ContainerConfig{
			SystemType:                 "Container",
			Name:                       id,
			Owner:                      owner,
			HostName:                   spec.Hostname,
			IgnoreFlushesDuringBoot:    spec.Windows.IgnoreFlushesDuringBoot,
			AllowUnqualifiedDNSQuery:   spec.Windows.Network.AllowUnqualifiedDNSQuery,
			EndpointList:               spec.Windows.Network.EndpointList,
			NetworkSharedContainerName: spec.Windows.Network.NetworkSharedContainerName,
		}
	)

	if spec.Windows.CredentialSpec != nil {
		conf.Credentials = spec.Windows.CredentialSpec.(string)
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
		conf.MappedDirectories = mds
	}

	if spec.Windows.Network.DNSSearchList != nil {
		conf.DNSSearchList = strings.Join(spec.Windows.Network.DNSSearchList, ",")
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

	var (
		err error
		di  = hcsshim.DriverInfo{
			Flavour: 1, // filter driver
			HomeDir: filepath.Dir(layerFolders[0]),
		}
	)

	// TODO: Once there is a snapshotter for windows, this can be deleted.
	// The R/W Layer should come from the Rootfs Mounts provided
	//
	// Windows doesn't support creating a container with a readonly
	// filesystem, so always create a RW one
	if err = hcsshim.CreateSandboxLayer(di, id, layerFolders[0], layerFolders); err != nil {
		return nil, errors.Wrapf(err, "failed to create sandbox layer for %s: layers: %#v, driverInfo: %#v",
			id, layerFolders, di)
	}
	conf.LayerFolderPath = filepath.Join(di.HomeDir, id)
	defer func() {
		if err != nil {
			removeLayer(ctx, conf.LayerFolderPath)
		}
	}()

	if err = hcsshim.ActivateLayer(di, id); err != nil {
		return nil, errors.Wrapf(err, "failed to activate layer %s", conf.LayerFolderPath)
	}

	if err = hcsshim.PrepareLayer(di, id, layerFolders); err != nil {
		return nil, errors.Wrapf(err, "failed to prepare layer %s", conf.LayerFolderPath)
	}

	conf.VolumePath, err = hcsshim.GetLayerMountPath(di, id)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to getmount path for layer %s: driverInfo: %#v", id, di)
	}

	return conf, nil
}

// removeLayer deletes the given layer, all associated containers must have
// been shutdown for this to succeed.
func removeLayer(ctx context.Context, path string) error {
	var (
		err        error
		layerID    = filepath.Base(path)
		parentPath = filepath.Dir(path)
		di         = hcsshim.DriverInfo{
			Flavour: 1, // filter driver
			HomeDir: parentPath,
		}
	)

	if err = hcsshim.UnprepareLayer(di, layerID); err != nil {
		log.G(ctx).WithError(err).Warnf("failed to unprepare layer %s for removal", path)
	}

	if err = hcsshim.DeactivateLayer(di, layerID); err != nil {
		log.G(ctx).WithError(err).Warnf("failed to deactivate layer %s for removal", path)
	}

	removePath := filepath.Join(parentPath, fmt.Sprintf("%s-removing", layerID))
	if err = os.Rename(path, removePath); err != nil {
		log.G(ctx).WithError(err).Warnf("failed to rename container layer %s for removal", path)
		removePath = path
	}

	if err = hcsshim.DestroyLayer(di, removePath); err != nil {
		log.G(ctx).WithError(err).Errorf("failed to remove container layer %s", removePath)
		return err
	}

	return nil
}

func newProcessConfig(spec *specs.Process, pset *pipeSet) *hcsshim.ProcessConfig {
	conf := &hcsshim.ProcessConfig{
		EmulateConsole:   pset.src.Terminal,
		CreateStdInPipe:  pset.stdin != nil,
		CreateStdOutPipe: pset.stdout != nil,
		CreateStdErrPipe: pset.stderr != nil,
		User:             spec.User.Username,
		CommandLine:      strings.Join(spec.Args, " "),
		Environment:      make(map[string]string),
		WorkingDirectory: spec.Cwd,
		ConsoleSize:      [2]uint{spec.ConsoleSize.Height, spec.ConsoleSize.Width},
	}

	// Convert OCI Env format to HCS's
	for _, s := range spec.Env {
		arr := strings.SplitN(s, "=", 2)
		if len(arr) == 2 {
			conf.Environment[arr[0]] = arr[1]
		}
	}

	return conf
}
