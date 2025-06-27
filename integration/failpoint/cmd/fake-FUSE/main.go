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

package main

import (
	"context"
	"flag"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/containerd/containerd/v2/pkg/fifosync"
	"github.com/containerd/log"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
)

const (
	fuseFSName               = "fakeFUSE"
	fuseMountPointName       = "fakeFUSERootDir"
	fuseFIFODirectoryName    = "FUSEFIFOs"
	fifoFileMode             = 0600
	fuseReadyFIFOFileName    = "fuseReadyTrigger.fifo"
	fuseCloseFIFOFileName    = "fuseCloseTrigger.fifo"
	fuseShutdownFIFOFileName = "fuseShutdownTrigger.fifo"

	statHangFIFOFileName = "statHang.fifo"
)

var logLevel = flag.String("log-level", log.DebugLevel.String(), "log level.")
var fuseDir = flag.String("fuse-dir", "fakeFUSEDir", "dir to be used by the FUSE process to store everything.")

// FUSE opts
var hangDuringStat = flag.Bool("hang-during-stat", false, "if set to true, hang-during-stat will hang the Stat call made to FUSE's root dir.")

// fuseFSOpts is a map of the name of a FUSE function (e.g. "Getattr") and the
// user-defined function to be executed at the beginning of FUSE function ("Getattr")
type fuseFSOpts map[string]func()

type FakeFUSEStruct struct {
	ctx     context.Context
	server  *fuse.Server
	fifoDir string
	// to trigger the caller to indicate that FUSE is ready to serve. The caller
	// must create a corresponding waiter (e.g. fuseReadyWaiter fifosync.Waiter)
	// and call fuseReadyWaiter.Wait() to understand when FUSE becomes ready and
	// then start the test(s).
	fuseReadyTrigger fifosync.Trigger
	// to wait for the caller to close FUSE's connection. The caller must create
	// a corresponding trigger (e.g. closeFuse fifosync.Trigger) and call
	// closeFuse.Trigger() to indicate that the FUSE process must be closed.
	fuseCloseWait fifosync.Waiter
	//to trigger the caller to indicate that FUSE has shut down completely.
	fuseShutdownTrigger fifosync.Trigger
	fuseMountPoint      string
	opts                fuseFSOpts
}

func main() {
	ctx := context.Background()
	flag.Parse()
	log.SetLevel(*logLevel)
	flag.VisitAll(func(f *flag.Flag) {
		log.G(ctx).Debugf("fakeFUSE flag %s: %s\n", f.Name, f.Value)
	})

	fuseDirectory := *fuseDir

	if err := os.RemoveAll(fuseDirectory); err != nil {
		log.G(ctx).Errorf("failed to remove an existing fuse directory (%v): %v", fuseDirectory, err)
	}
	if err := os.MkdirAll(fuseDirectory, os.ModeDir); err != nil {
		log.G(ctx).Errorf("failed to create a fuse directory (%v): %v", fuseDirectory, err)
	}
	// If the user has provided the fuseDir, then they are responsible to manage it
	if *fuseDir == "" {
		defer os.RemoveAll(fuseDirectory)
	}

	fuseStruct := createFUSEFS(ctx)
	log.G(ctx).Debugf("fuse struct: %v", fuseStruct)

	// Trigger readiness after the server is successfully mounted and before serving.
	log.G(ctx).Info("triggering ready via fifo")
	if err := fuseStruct.fuseReadyTrigger.Trigger(); err != nil {
		log.G(ctx).Fatalf("failed to trigger FUSE's readiness: %v", err)
	}

	go func() {
		log.G(ctx).Infof("starting a FUSE server at: %v", fuseStruct.fuseMountPoint)
		fuseStruct.server.Serve()
	}()

	fuseStruct.fuseCloseWait.Wait()
	closeFuseConn(ctx, fuseStruct)
	log.G(ctx).Infof("FUSE server has been shut down")
}

func createFUSEFS(ctx context.Context) FakeFUSEStruct {
	// Create a directory to act as the mount point for FUSE
	fuseMountPointDir := filepath.Join(*fuseDir, fuseMountPointName)
	if err := os.MkdirAll(fuseMountPointDir, os.ModePerm); err != nil {
		log.G(ctx).Fatalf("failed to create a fuse mount point (%v): %v", fuseMountPointDir, err)
	}

	// Create a directory to store all the FIFOs
	fuseFIFODir := filepath.Join(*fuseDir, fuseFIFODirectoryName)
	if err := os.MkdirAll(fuseFIFODir, os.ModePerm); err != nil {
		log.G(ctx).Fatalf("failed to create the FIFOs directory (%v): %v", fuseFIFODir, err)
	}

	// Create the FUSE ready FIFO
	fuseReadyFIFOFile := filepath.Join(fuseFIFODir, fuseReadyFIFOFileName)
	fuseReadyTrigger, err := fifosync.NewTrigger(fuseReadyFIFOFile, fifoFileMode)
	if err != nil {
		log.G(ctx).Fatalf("failed to create a FUSE fifo ready trigger (%v): %v", fuseReadyFIFOFile, err)
	}

	// Create the FUSE close FIFO
	fuseCloseFIFOFile := filepath.Join(fuseFIFODir, fuseCloseFIFOFileName)
	fuseCloseWaiter, err := fifosync.NewWaiter(fuseCloseFIFOFile, fifoFileMode)
	if err != nil {
		log.G(ctx).Fatalf("failed to create a FUSE fifo close waiter (%v): %v", fuseCloseFIFOFile, err)
	}

	// Create the FUSE shutdown trigger FIFO
	fuseShutdownFIFOFile := filepath.Join(fuseFIFODir, fuseShutdownFIFOFileName)
	fuseShutdownTrigger, err := fifosync.NewTrigger(fuseShutdownFIFOFile, fifoFileMode)
	if err != nil {
		log.G(ctx).Fatalf("failed to create a FUSE fifo shutdown trigger (%v): %v", fuseShutdownFIFOFile, err)
	}

	log.G(ctx).Debugf("created FUSE FIFOs")

	var fuseOpts fuseFSOpts
	if *hangDuringStat {
		log.G(ctx).Info("setting up FUSE to hang during Getattr")
		var statHangWait fifosync.Waiter
		statHangFIFOFile := filepath.Join(fuseFIFODir, statHangFIFOFileName)
		if statHangWait, err = fifosync.NewWaiter(statHangFIFOFile, fifoFileMode); err != nil {
			log.G(ctx).Fatalf("failed to create a stat hang waiter (%v): %v", statHangFIFOFile, err)
		}

		fuseOpts = fuseFSOpts{
			"Getattr": func() {
				log.G(ctx).Infof("waiting during Getattr to repro issue: %v", statHangWait.Name())
				if err := statHangWait.Wait(); err != nil {
					log.G(ctx).Fatalf("failed to wait in Getattr: %v", err)
				}
			},
		}
	}

	// Instantiate the root of the FUSE FS
	root := &FUSEFSRoot{opts: fuseOpts}
	attrTimeout := 60 * time.Second
	fsOpts := &fs.Options{
		MountOptions: fuse.MountOptions{
			Name:   fuseFSName,
			Debug:  *logLevel == log.DebugLevel.String(),
			FsName: fuseFSName,
		},
		AttrTimeout: &attrTimeout,
	}

	// Create the FUSE server
	rawFS := fs.NewNodeFS(root, fsOpts)
	server, err := fuse.NewServer(rawFS, fuseMountPointDir, &fsOpts.MountOptions)
	if err != nil {
		log.G(ctx).Fatalf("failed to create a FUSE server at %v: %v", fuseMountPointDir, err)
	}
	log.G(ctx).Debugf("created the FUSE mount point: %v", fuseMountPointDir)

	fakeFUSEStruct := FakeFUSEStruct{
		ctx:                 ctx,
		server:              server,
		fifoDir:             fuseFIFODir,
		fuseReadyTrigger:    fuseReadyTrigger,
		fuseCloseWait:       fuseCloseWaiter,
		fuseShutdownTrigger: fuseShutdownTrigger,
		fuseMountPoint:      fuseMountPointDir,
		opts:                fuseOpts,
	}
	return fakeFUSEStruct
}

func closeFuseConn(ctx context.Context, fuseStruct FakeFUSEStruct) {
	log.G(ctx).Infof("shutting down the FUSE server at mountpoint %s", fuseStruct.fuseMountPoint)
	defer fuseStruct.fuseShutdownTrigger.Trigger()

	if err := fuseStruct.server.Unmount(); err != nil {
		log.G(ctx).Fatalf("failed to unmount the mountpoint (%v): %v", fuseStruct.fuseMountPoint, err)
	}

	if *fuseDir == "" {
		if err := os.RemoveAll(*fuseDir); err != nil {
			log.G(ctx).Fatalf("failed to remove the fuse dir (%v): %v", *fuseDir, err)
		}
	}
}

type FUSEFSRoot struct {
	fs.Inode
	opts fuseFSOpts
}

// FUSEFSRoot implements the NodeGetattrer interface.
var _ = (fs.NodeGetattrer)((*FUSEFSRoot)(nil))

// Getattr is called by the kernel to retrieve file attributes.
func (fr *FUSEFSRoot) Getattr(ctx context.Context, fh fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	if *hangDuringStat && fr.opts != nil {
		log.G(ctx).Infof("Will hang during Getattr")
		if fn, ok := fr.opts["Getattr"]; ok {
			fn()
		}
	} else {
		log.G(ctx).Infof("Will not hang during Getattr")
	}

	// Set the attributes for the root directory.
	out.Mode = uint32(os.ModeDir | 0o555)
	out.Ino = 1 // Root inode
	return fs.OK
}
