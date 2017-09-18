// +build windows

package windows

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Microsoft/hcsshim"
	opengcs "github.com/Microsoft/opengcs/client"
	"github.com/containerd/containerd/log"
	"github.com/pkg/errors"
)

// TODO(darrenstahlmsft) most of the functions in this file belong in the snapshotter.
// This is a temporary location, and this should all be moved when the snapshotter is implemented.

const (
	// sandboxFilename is the name of the file containing a layer's sandbox (read-write layer).
	sandboxFilename = "sandbox.vhdx"

	// scratchFilename is the name of the scratch-space used by an SVM to avoid running out of memory.
	scratchFilename = "scratch.vhdx"

	// layerFilename is the name of the file containing a layer's read-only contents.
	// Note this really is VHD format, not VHDX.
	layerFilename = "layer.vhd"

	// toolsScratchPath is a location in a service utility VM that the tools can use as a
	// scratch space to avoid running out of memory.
	toolsScratchPath = "/tmp/scratch"

	// svmGlobalID is the ID used in the serviceVMs map for the global service VM when running in "global" mode.
	svmGlobalID = "_lcow_global_svm_"

	// cacheDirectory is the sub-folder under the driver's data-root used to cache blank sandbox and scratch VHDs.
	cacheDirectory = "cache"

	// scratchDirectory is the sub-folder under the driver's data-root used for scratch VHDs in service VMs
	scratchDirectory = "scratch"
)

// cacheItem is our internal structure representing an item in our local cache
// of things that have been mounted.
type cacheItem struct {
	sync.Mutex        // Protects operations performed on this item
	uvmPath    string // Path in utility VM
	hostPath   string // Path on host
	refCount   int    // How many times its been mounted
	isSandbox  bool   // True if a sandbox
	isMounted  bool   // True when mounted in a service VM
}

// setIsMounted is a helper function for a cacheItem which does exactly what it says
func (ci *cacheItem) setIsMounted() {
	ci.Lock()
	defer ci.Unlock()
	ci.isMounted = true
}

// incrementRefCount is a helper function for a cacheItem which does exactly what it says
func (ci *cacheItem) incrementRefCount() {
	ci.Lock()
	defer ci.Unlock()
	ci.refCount++
}

// decrementRefCount is a helper function for a cacheItem which does exactly what it says
func (ci *cacheItem) decrementRefCount() int {
	ci.Lock()
	defer ci.Unlock()
	ci.refCount--
	return ci.refCount
}

// serviceVMItem is our internal structure representing an item in our
// map of service VMs we are maintaining.
type serviceVMItem struct {
	sync.Mutex                      // Serialises operations being performed in this service VM.
	scratchAttached bool            // Has a scratch been attached?
	config          *opengcs.Config // Represents the service VM item.
}

// startServiceVM starts a service utility VM if it is not currently running.
// It can optionally be started with a mapped virtual disk.
func (r *LCOWRuntime) startServiceVM(ctx context.Context, id string, mvdToAdd *hcsshim.MappedVirtualDisk, context string) (*serviceVMItem, error) {
	// Use the global ID if in global mode
	if r.globalMode {
		id = svmGlobalID
	}

	title := fmt.Sprintf("lcowdriver: startServiceVM %s:", id)

	// Make sure thread-safe when interrogating the map
	log.G(ctx).Debugf("%s taking serviceVmsMutex", title)
	r.serviceVmsMutex.Lock()

	// Nothing to do if it's already running except add the mapped drive if supplied.
	if svm, ok := r.serviceVms[id]; ok {
		log.G(ctx).Debugf("%s exists, releasing serviceVmsMutex", title)
		r.serviceVmsMutex.Unlock()

		if mvdToAdd != nil {
			log.G(ctx).Debugf("hot-adding %s to %s", mvdToAdd.HostPath, mvdToAdd.ContainerPath)

			// Ensure the item is locked while doing this
			log.G(ctx).Debugf("%s locking serviceVmItem %s", title, svm.config.Name)
			svm.Lock()
			defer svm.Unlock()

			if err := svm.config.HotAddVhd(mvdToAdd.HostPath, mvdToAdd.ContainerPath, false, true); err != nil {
				log.G(ctx).Debugf("%s releasing serviceVmItem %s on hot-add failure %s", title, svm.config.Name, err)
				return nil, errors.Wrapf(err, "hot add vhd %s to %s failed", mvdToAdd.HostPath, mvdToAdd.ContainerPath)
			}

			log.G(ctx).Debugf("%s releasing serviceVmItem %s", title, svm.config.Name)
		}
		return svm, nil
	}

	// Release the lock early
	log.G(ctx).Debugf("%s releasing serviceVmsMutex", title)
	r.serviceVmsMutex.Unlock()

	// So we are starting one. First need an enpty structure.
	svm := &serviceVMItem{
		config: &opengcs.Config{},
	}

	//TODO(darrenstahlmsft) add LCOW options
	// Generate a default configuration
	if err := svm.config.GenerateDefault(nil); err != nil {
		return nil, fmt.Errorf("%s failed to generate default gogcs configuration for global svm (%s): %s", title, context, err)
	}

	// For the name, we deliberately suffix if safe-mode to ensure that it doesn't
	// clash with another utility VM which may be running for the container itself.
	// This also makes it easier to correlate through Get-ComputeProcess.
	if id == svmGlobalID {
		svm.config.Name = svmGlobalID
	} else {
		svm.config.Name = fmt.Sprintf("%s_svm", id)
	}

	// Ensure we take the cached scratch mutex around the check to ensure the file is complete
	// and not in the process of being created by another thread.
	scratchTargetFile := filepath.Join(r.root, scratchDirectory, fmt.Sprintf("%s.vhdx", id))

	log.G(ctx).Debugf("%s locking cachedScratchMutex", title)
	r.cachedScratchMutex.Lock()
	if _, err := os.Stat(r.cachedScratchFile); err == nil {
		// Make a copy of cached scratch to the scratch directory
		log.G(ctx).Debugf("lcowdriver: startServiceVM: (%s) cloning cached scratch for mvd", context)
		if err := opengcs.CopyFile(r.cachedScratchFile, scratchTargetFile, true); err != nil {
			log.G(ctx).Debugf("%s releasing cachedScratchMutex on err: %s", title, err)
			r.cachedScratchMutex.Unlock()
			return nil, err
		}

		// Add the cached clone as a mapped virtual disk
		log.G(ctx).Debugf("lcowdriver: startServiceVM: (%s) adding cloned scratch as mvd", context)
		mvd := hcsshim.MappedVirtualDisk{
			HostPath:          scratchTargetFile,
			ContainerPath:     toolsScratchPath,
			CreateInUtilityVM: true,
		}
		svm.config.MappedVirtualDisks = append(svm.config.MappedVirtualDisks, mvd)
		svm.scratchAttached = true
	}
	log.G(ctx).Debugf("%s releasing cachedScratchMutex", title)
	r.cachedScratchMutex.Unlock()

	// If requested to start it with a mapped virtual disk, add it now.
	if mvdToAdd != nil {
		svm.config.MappedVirtualDisks = append(svm.config.MappedVirtualDisks, *mvdToAdd)
	}

	// Start it.
	log.G(ctx).Debugf("lcowdriver: startServiceVM: (%s) starting %s", context, svm.config.Name)
	if err := svm.config.StartUtilityVM(); err != nil {
		return nil, fmt.Errorf("failed to start service utility VM (%s): %s", context, err)
	}

	// As it's now running, add it to the map, checking for a race where another
	// thread has simultaneously tried to start it.
	log.G(ctx).Debugf("%s locking serviceVmsMutex for insertion", title)
	r.serviceVmsMutex.Lock()
	if svm, ok := r.serviceVms[id]; ok {
		log.G(ctx).Debugf("%s releasing serviceVmsMutex after insertion but exists", title)
		r.serviceVmsMutex.Unlock()
		return svm, nil
	}
	r.serviceVms[id] = svm
	log.G(ctx).Debugf("%s releasing serviceVmsMutex after insertion", title)
	r.serviceVmsMutex.Unlock()

	// Now we have a running service VM, we can create the cached scratch file if it doesn't exist.
	log.G(ctx).Debugf("%s locking cachedScratchMutex", title)
	r.cachedScratchMutex.Lock()
	if !svm.scratchAttached {
		log.G(ctx).Debugf("%s (%s): creating an SVM scratch - locking serviceVM", title, context)
		svm.Lock()
		if err := svm.config.CreateExt4Vhdx(scratchTargetFile, opengcs.DefaultVhdxSizeGB, r.cachedScratchFile); err != nil {
			log.G(ctx).Debugf("%s (%s): releasing serviceVM on error path from CreateExt4Vhdx: %s", title, context, err)
			svm.Unlock()
			log.G(ctx).Debugf("%s (%s): releasing cachedScratchMutex on error path", title, context)
			r.cachedScratchMutex.Unlock()

			// Do a force terminate and remove it from the map on failure, ignoring any errors
			if err2 := r.terminateServiceVM(ctx, id, "error path from CreateExt4Vhdx", true); err2 != nil {
				log.G(ctx).Warnf("failed to terminate service VM on error path from CreateExt4Vhdx: %s", err2)
			}

			return nil, fmt.Errorf("failed to create SVM scratch VHDX (%s): %s", context, err)
		}
		log.G(ctx).Debugf("%s (%s): releasing serviceVM after %s created and cached to %s", title, context, scratchTargetFile, r.cachedScratchFile)
		svm.Unlock()
	}
	log.G(ctx).Debugf("%s (%s): releasing cachedScratchMutex", title, context)
	r.cachedScratchMutex.Unlock()

	// Hot-add the scratch-space if not already attached
	if !svm.scratchAttached {
		log.G(ctx).Debugf("lcowdriver: startServiceVM: (%s) hot-adding scratch %s - locking serviceVM", context, scratchTargetFile)
		svm.Lock()
		if err := svm.config.HotAddVhd(scratchTargetFile, toolsScratchPath, false, true); err != nil {
			log.G(ctx).Debugf("%s (%s): releasing serviceVM on error path of HotAddVhd: %s", title, context, err)
			svm.Unlock()

			// Do a force terminate and remove it from the map on failure, ignoring any errors
			if err2 := r.terminateServiceVM(ctx, id, "error path from HotAddVhd", true); err2 != nil {
				log.G(ctx).Warnf("failed to terminate service VM on error path from HotAddVhd: %s", err2)
			}

			return nil, fmt.Errorf("failed to hot-add %s failed: %s", scratchTargetFile, err)
		}
		log.G(ctx).Debugf("%s (%s): releasing serviceVM", title, context)
		svm.scratchAttached = true
		svm.Unlock()
	}

	log.G(ctx).Debugf("lcowdriver: startServiceVM: (%s) success", context)
	return svm, nil
}

// getServiceVM returns the appropriate service utility VM instance, optionally
// deleting it from the map (but not the global one)
func (r *LCOWRuntime) getServiceVM(ctx context.Context, id string, deleteFromMap bool) *serviceVMItem {
	log.G(ctx).Debugf("lcowdriver: getservicevm:locking serviceVmsMutex")
	r.serviceVmsMutex.Lock()
	defer func() {
		log.G(ctx).Debugf("lcowdriver: getservicevm:releasing serviceVmsMutex")
		r.serviceVmsMutex.Unlock()
	}()
	if r.globalMode {
		id = svmGlobalID
	}
	if _, ok := r.serviceVms[id]; !ok {
		return nil
	}
	svm := r.serviceVms[id]
	if deleteFromMap && id != svmGlobalID {
		log.G(ctx).Debugf("lcowdriver: getservicevm: removing %s from map", id)
		delete(r.serviceVms, id)
	}
	return svm
}

// terminateServiceVM terminates a service utility VM if its running, but does nothing
// when in global mode as it's lifetime is limited to that of the daemon.
func (r *LCOWRuntime) terminateServiceVM(ctx context.Context, id, context string, force bool) error {

	// We don't do anything in safe mode unless the force flag has been passed, which
	// is only the case for cleanup at driver termination.
	if r.globalMode {
		if !force {
			log.G(ctx).Debugf("lcowdriver: terminateservicevm: %s (%s) - doing nothing as in global mode", id, context)
			return nil
		}
		id = svmGlobalID
	}

	// Get the service VM and delete it from the map
	svm := r.getServiceVM(ctx, id, true)
	if svm == nil {
		return errors.New("unable to get service VM")
	}

	// We run the deletion of the scratch as a deferred function to at least attempt
	// clean-up in case of errors.
	defer func() {
		if svm.scratchAttached {
			scratchTargetFile := filepath.Join(r.root, scratchDirectory, fmt.Sprintf("%s.vhdx", id))
			log.G(ctx).Debugf("lcowdriver: terminateservicevm: %s (%s) - deleting scratch %s", id, context, scratchTargetFile)
			if err := os.Remove(scratchTargetFile); err != nil {
				log.G(ctx).Warnf("failed to remove scratch file %s (%s): %s", scratchTargetFile, context, err)
			}
		}
	}()

	// Nothing to do if it's not running
	if svm.config.Uvm != nil {
		log.G(ctx).Debugf("lcowdriver: terminateservicevm: %s (%s) - calling terminate", id, context)
		if err := svm.config.Uvm.Terminate(); err != nil {
			return fmt.Errorf("failed to terminate utility VM (%s): %s", context, err)
		}

		log.G(ctx).Debugf("lcowdriver: terminateservicevm: %s (%s) - waiting for utility VM to terminate", id, context)
		if err := svm.config.Uvm.WaitTimeout(time.Duration(svm.config.UvmTimeoutSeconds) * time.Second); err != nil {
			return fmt.Errorf("failed waiting for utility VM to terminate (%s): %s", context, err)
		}
	}

	log.G(ctx).Debugf("lcowdriver: terminateservicevm: %s (%s) - success", id, context)
	return nil
}

// CreateRWLayer creates a layer that is writable for use as a container
// file system. That equates to creating a sandbox.
func (r *LCOWRuntime) CreateRWLayer(ctx context.Context, id, parent string) error {
	title := fmt.Sprintf("lcowdriver: CreateRWLayer: id %s", id)
	log.G(ctx).Debugf(title)

	// First we need to create the folder
	if err := r.CreateLayerFolder(id, parent); err != nil {
		return err
	}

	// Look for an explicit sandbox size option.
	sandboxSize := uint64(opengcs.DefaultVhdxSizeGB)

	// Massive perf optimisation here. If we know that the RW layer is the default size,
	// and that the cached sandbox already exists, and we are running in safe mode, we
	// can just do a simple copy into the layers sandbox file without needing to start a
	// unique service VM. For a global service VM, it doesn't really matter. Of course,
	// this is only the case where the sandbox is the default size.
	//
	// Make sure we have the sandbox mutex taken while we are examining it.
	if sandboxSize == opengcs.DefaultVhdxSizeGB {
		log.G(ctx).Debugf("%s: locking cachedSandboxMutex", title)
		r.cachedSandboxMutex.Lock()
		_, err := os.Stat(r.cachedSandboxFile)
		log.G(ctx).Debugf("%s: releasing cachedSandboxMutex", title)
		r.cachedSandboxMutex.Unlock()
		if err == nil {
			log.G(ctx).Debugf("%s: using cached sandbox to populate", title)
			if err := opengcs.CopyFile(r.cachedSandboxFile, filepath.Join(r.containerLayerDir(id), sandboxFilename), true); err != nil {
				return err
			}
			return nil
		}
	}

	log.G(ctx).Debugf("%s: creating SVM to create sandbox", title)
	svm, err := r.startServiceVM(ctx, id, nil, "CreateRWLayer")
	if err != nil {
		return err
	}
	defer r.terminateServiceVM(ctx, id, "CreateRWLayer", false)

	// So the sandbox needs creating. If default size ensure we are the only thread populating the cache.
	// Non-default size we don't store, just create them one-off so no need to lock the cachedSandboxMutex.
	if sandboxSize == opengcs.DefaultVhdxSizeGB {
		log.G(ctx).Debugf("%s: locking cachedSandboxMutex for creation", title)
		r.cachedSandboxMutex.Lock()
		defer func() {
			log.G(ctx).Debugf("%s: releasing cachedSandboxMutex for creation", title)
			r.cachedSandboxMutex.Unlock()
		}()
	}

	// Synchronise the operation in the service VM.
	log.G(ctx).Debugf("%s: locking svm for sandbox creation", title)
	svm.Lock()
	defer func() {
		log.G(ctx).Debugf("%s: releasing svm for sandbox creation", title)
		svm.Unlock()
	}()

	// Make sure we don't write to our local cached copy if this is for a non-default size request.
	targetCacheFile := r.cachedSandboxFile
	if sandboxSize != opengcs.DefaultVhdxSizeGB {
		targetCacheFile = ""
	}

	// Actually do the creation.
	if err := svm.config.CreateExt4Vhdx(filepath.Join(r.containerLayerDir(id), sandboxFilename), uint32(sandboxSize), targetCacheFile); err != nil {
		return err
	}

	return nil
}

// dir returns the absolute path to the layer.
func (r *LCOWRuntime) containerLayerDir(id string) string {
	return filepath.Join(r.root, filepath.Base(id))
}

// CreateLayerFolder creates the folder for the layer with the given id, and
// adds it to the layer chain.
func (r *LCOWRuntime) CreateLayerFolder(id, parent string) error {
	layerPath := r.containerLayerDir(id)
	if err := mkdirAllWithACL(layerPath, 755, sddlNtvmAdministratorsLocalSystem); err != nil {
		return err
	}
	return nil
}
