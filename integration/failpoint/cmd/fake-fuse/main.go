//go:build linux

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
	"os/signal"
	"syscall"

	"os"
	"path/filepath"

	"github.com/containerd/containerd/v2/pkg/fifosync"
	"github.com/containerd/log"

	"bazil.org/fuse"
	fusefs "bazil.org/fuse/fs"
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

// fuseFSOpts is a map of the name of a FUSE function (e.g. "Attr") and the
// user-defined function to be executed at the beginning of FUSE function ("Attr")
type fuseFSOpts map[string]func(context.Context) error

type FakeFUSEStruct struct {
	ctx      context.Context
	fuseConn *fuse.Conn
	fifoDir  string
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

type FUSEFSRoot struct {
	opts fuseFSOpts
}

func main() {
	// This ensures the program handles OS signals gracefully.
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	log.SetLevel(*logLevel)
	flag.Parse()
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
	// If the user has provided the fuseDir, then they are responsible to manage it.
	if *fuseDir == "" {
		defer os.RemoveAll(fuseDirectory)
	}

	fuseStruct := createFUSEFS(ctx)
	log.G(ctx).Debugf("fuse struct: %v", fuseStruct)

	go func() {
		log.G(ctx).Infof("starting a FUSE server at: %v", fuseStruct.fuseMountPoint)
		if err := fusefs.Serve(fuseStruct.fuseConn, fuseStruct); err != nil {
			log.G(ctx).Errorf("Server shut down unexpectedly: %v", err)
		} else {
			log.G(ctx).Infof("shutting down the fuse server")
		}
	}()

	waitCh := make(chan error, 1)
	go func() {
		waitCh <- fuseStruct.fuseCloseWait.Wait()
	}()

	select {
	case <-ctx.Done():
		log.G(ctx).Info("Context cancelled or timed out, shutting down FUSE...")
	case err := <-waitCh:
		if err != nil {
			log.G(ctx).Errorf("Error waiting for close signal: %v", err)
		} else {
			log.G(ctx).Info("Received close signal from caller")
		}
	}

	closeFuseConn(ctx, fuseStruct)
}

func createFUSEFS(ctx context.Context) FakeFUSEStruct {
	// Create a directory to act as the mount point for FUSE
	fuseMountPointDir := filepath.Join(*fuseDir, fuseMountPointName)
	if err := os.MkdirAll(fuseMountPointDir, os.ModeDir); err != nil {
		log.G(ctx).Fatalf("failed to create a fuse mount point (%v): %v", fuseMountPointDir, err)
	}
	// Mount the FUSE FS
	fuseConn, err := fuse.Mount(fuseMountPointDir, fuse.FSName(fuseFSName))
	if err != nil {
		log.G(ctx).Fatalf("failed to create a FUSE connection at %v: %v", fuseMountPointDir, err)
	}
	log.G(ctx).Debugf("created the FUSE mount point: %v", fuseMountPointDir)

	// Create a directory to store all the FIFOs
	fuseFIFODir := filepath.Join(*fuseDir, fuseFIFODirectoryName)
	if err := os.MkdirAll(fuseFIFODir, os.ModeDir); err != nil {
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
		log.G(ctx).Info("setting up FUSE to hang during Stat")
		var statHangWait fifosync.Waiter
		var err error
		statHangFIFOFile := filepath.Join(fuseFIFODir, statHangFIFOFileName)
		if statHangWait, err = fifosync.NewWaiter(statHangFIFOFile, fifoFileMode); err != nil {
			log.G(ctx).Fatalf("failed to create a stat hang waiter (%v): %v", statHangFIFOFile, err)
		}

		fuseOpts = fuseFSOpts{
			"Attr": func(ctx context.Context) error {
				log.G(ctx).Infof("waiting during Stat to repro issue 10828: %v", statHangWait.Name())
				errCh := make(chan error, 1)
				go func() {
					errCh <- statHangWait.Wait()
				}()

				select {
				case err := <-errCh:
					if err != nil {
						log.G(ctx).Errorf("failed to wait in Stat: %v", err)
					}
					return err
				case <-ctx.Done():
					log.G(ctx).Info("Stat call interrupted or timed out by caller")
					return ctx.Err()
				}
			},
		}
	}

	fakeFUSEStruct := FakeFUSEStruct{
		ctx:                 ctx,
		fuseConn:            fuseConn,
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
	log.G(ctx).Infof("shutting down the FUSE server")

	if err := fuse.Unmount(fuseStruct.fuseMountPoint); err != nil {
		log.G(ctx).Errorf("failed to unmount the mountpoint (%v): %v", fuseStruct.fuseMountPoint, err)
		// Fallback: Lazy unmount ensures the mount point is detached even if busy,
		// preventing "transport endpoint not connected" errors for the caller.
		_ = syscall.Unmount(fuseStruct.fuseMountPoint, syscall.MNT_DETACH)
	}

	if err := fuseStruct.fuseConn.Close(); err != nil {
		log.G(ctx).Errorf("failed to close the fuse connection: %v", err)
	}

	if *fuseDir == "" {
		if err := os.RemoveAll(*fuseDir); err != nil {
			log.G(ctx).Errorf("failed to remove the fuse dir (%v): %v", *fuseDir, err)
		}
	}

	log.G(ctx).Info("FUSE cleanup complete, signaling shutdown FIFO")
	if err := fuseStruct.fuseShutdownTrigger.Trigger(); err != nil {
		log.G(ctx).Errorf("failed to trigger shutdown FIFO: %v", err)
	}
}

func (fs FakeFUSEStruct) Root() (fusefs.Node, error) {
	log.G(fs.ctx).Info("triggering ready via fifo")
	err := fs.fuseReadyTrigger.Trigger()
	if err != nil {
		log.G(fs.ctx).Fatalf("failed to trigger FUSE's readiness: %v", err)
	}
	return &FUSEFSRoot{opts: fs.opts}, nil
}

// This is required as the kernel stats the root directory.
func (fr *FUSEFSRoot) Attr(ctx context.Context, a *fuse.Attr) error {
	if *hangDuringStat && fr.opts != nil {
		log.G(ctx).Infof("Will hang during Stat")
		if fn, ok := fr.opts["Attr"]; ok {
			if err := fn(ctx); err != nil {
				return err
			}
		}
	} else {
		log.G(ctx).Infof("Will not hang during Stat")
	}
	a.Inode = 1 // Root inode
	a.Mode = os.ModeDir | 0o555
	return nil
}
