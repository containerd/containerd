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

type fakeFUSE struct {
	ctx      context.Context
	fuseConn *fuse.Conn
	fifoDir  string
	// fuseReadyTrigger is used to trigger the caller to indicate that FUSE
	// is ready to serve. The caller must create a corresponding waiter (e.g.
	// fuseReadyWaiter fifosync.Waiter) and call fuseReadyWaiter.Wait() to
	// understand when FUSE becomes ready and then start the test(s).
	fuseReadyTrigger fifosync.Trigger
	// fuseCloseWait is used to wait for the caller to close FUSE's connection.
	// The caller must create a corresponding trigger (e.g. closeFuse
	// fifosync.Trigger) and call closeFuse.Trigger() to indicate that the FUSE
	// process must be closed.
	fuseCloseWait fifosync.Waiter
	// fuseShutdownTrigger is used to trigger the caller to indicate that FUSE
	// has shut down completely.
	fuseShutdownTrigger fifosync.Trigger
	fuseMountPoint      string
	// attr defines the attr fn to run when a request to get file attributes is
	// made to FUSE.
	attr func(context.Context, *fuse.Attr) error
}

type fuseFSRoot struct {
	attr func(context.Context, *fuse.Attr) error
}

func main() {
	// This ensures the program handles OS signals gracefully.
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	log.SetLevel(*logLevel)
	flag.Parse()
	flag.VisitAll(func(f *flag.Flag) {
		log.G(ctx).WithField("flag", f.Name).WithField("value", f.Value).Debug("fakeFUSE flag")
	})

	// Since this FUSE code is introduced to test PR#12023 for issue#10828, we
	// make the "hang-during-stat" flag mandatory. If this code is being also
	// used for some other purpose, this can be removed.
	if !*hangDuringStat {
		log.G(ctx).Error("--hang-during-stat is required")
		os.Exit(1)
	}

	if *fuseDir == "" {
		log.G(ctx).Error("fuse directory cannot be empty. It can be set via the --fuse-dir flag.")
		os.Exit(1)
	}

	fuseDirectory := *fuseDir
	if err := os.RemoveAll(fuseDirectory); err != nil {
		log.G(ctx).WithField("fuse_dir", fuseDirectory).WithError(err).Error("failed to remove an existing FUSE directory")
	}
	if err := os.MkdirAll(fuseDirectory, os.ModeDir); err != nil {
		log.G(ctx).WithField("fuse_dir", fuseDirectory).WithError(err).Error("failed to create a FUSE directory")
	}

	fuseStruct := createFUSEFS(ctx)
	log.G(ctx).Debugf("fuse struct: %v", fuseStruct)

	go func() {
		log.G(ctx).WithField("fuse_mount_point", fuseStruct.fuseMountPoint).Info("starting a FUSE server")
		if err := fusefs.Serve(fuseStruct.fuseConn, fuseStruct); err != nil {
			log.G(ctx).WithError(err).Error("FUSE server shut down unexpectedly")
		} else {
			log.G(ctx).Infof("shutting down the FUSE server")
		}
	}()

	err := fuseStruct.fuseCloseWait.WaitCtx(ctx)
	if err != nil {
		log.G(ctx).WithError(err).Error("error waiting for close signal")
	} else {
		log.G(ctx).Info("received close signal from caller")
	}

	fuseStruct.Close()
}

func createFUSEFS(ctx context.Context) fakeFUSE {
	// Create a directory to act as the mount point for FUSE
	fuseMountPointDir := filepath.Join(*fuseDir, fuseMountPointName)
	if err := os.MkdirAll(fuseMountPointDir, os.ModeDir); err != nil {
		log.G(ctx).WithField("fuse_mount_point_dir", fuseMountPointDir).WithError(err).Fatal("failed to create a FUSE mount point")
	}
	// Mount the FUSE FS
	fuseConn, err := fuse.Mount(fuseMountPointDir, fuse.FSName(fuseFSName))
	if err != nil {
		log.G(ctx).WithField("fuse_mount_point_dir", fuseMountPointDir).WithError(err).Fatal("failed to create a FUSE connection")
	}
	log.G(ctx).WithField("fuse_mount_point_dir", fuseMountPointDir).Debug("created the FUSE mount point")

	// Create a directory to store all the FIFOs
	fuseFIFODir := filepath.Join(*fuseDir, fuseFIFODirectoryName)
	if err := os.MkdirAll(fuseFIFODir, os.ModeDir); err != nil {
		log.G(ctx).WithField("fuse_fifo_dir", fuseFIFODir).WithError(err).Fatal("failed to create the FIFOs directory")
	}

	// Create the FUSE ready FIFO
	fuseReadyFIFOFile := filepath.Join(fuseFIFODir, fuseReadyFIFOFileName)
	fuseReadyTrigger, err := fifosync.NewTrigger(fuseReadyFIFOFile, fifoFileMode)
	if err != nil {
		log.G(ctx).WithField("fuse_ready_fifo", fuseReadyFIFOFile).WithError(err).Fatal("failed to create a FUSE fifo ready trigger")
	}

	// Create the FUSE close FIFO
	fuseCloseFIFOFile := filepath.Join(fuseFIFODir, fuseCloseFIFOFileName)
	fuseCloseWaiter, err := fifosync.NewWaiter(fuseCloseFIFOFile, fifoFileMode)
	if err != nil {
		log.G(ctx).WithField("fuse_close_fifo", fuseReadyFIFOFile).WithError(err).Fatal("failed to create a FUSE fifo close waiter")
	}

	// Create the FUSE shutdown trigger FIFO
	fuseShutdownFIFOFile := filepath.Join(fuseFIFODir, fuseShutdownFIFOFileName)
	fuseShutdownTrigger, err := fifosync.NewTrigger(fuseShutdownFIFOFile, fifoFileMode)
	if err != nil {
		log.G(ctx).WithField("fuse_shutdown_fifo", fuseShutdownFIFOFile).WithError(err).Fatal("failed to create a FUSE fifo shutdown trigger")
	}

	log.G(ctx).Debugf("created FUSE FIFOs")

	attrFn := func(context.Context, *fuse.Attr) error { return nil }
	if *hangDuringStat {
		log.G(ctx).Info("setting up FUSE to hang during Stat")
		var (
			statHangWait fifosync.Waiter
			err          error
		)
		statHangFIFOFile := filepath.Join(fuseFIFODir, statHangFIFOFileName)
		if statHangWait, err = fifosync.NewWaiter(statHangFIFOFile, fifoFileMode); err != nil {
			log.G(ctx).WithField("stat_hang_fifo", statHangFIFOFile).WithError(err).Fatal("failed to create a stat hang waiter")
		}

		attrFn = func(ctx context.Context, a *fuse.Attr) error {
			log.G(ctx).WithField("stat_hang_wait", statHangWait.Name()).Info("waiting during stat to repro issue 10828")
			errCh := make(chan error, 1)
			go func() {
				errCh <- statHangWait.Wait()
			}()

			err := statHangWait.WaitCtx(ctx)
			if err != nil {
				log.G(ctx).WithError(err).Error("failed to wait in stat")
			}
			return err
		}
	}

	fakeFUSEStruct := fakeFUSE{
		ctx:                 ctx,
		fuseConn:            fuseConn,
		fifoDir:             fuseFIFODir,
		fuseReadyTrigger:    fuseReadyTrigger,
		fuseCloseWait:       fuseCloseWaiter,
		fuseShutdownTrigger: fuseShutdownTrigger,
		fuseMountPoint:      fuseMountPointDir,
		attr:                attrFn,
	}
	return fakeFUSEStruct
}

// Close closes the FUSE connection.
func (fs fakeFUSE) Close() {
	log.G(fs.ctx).Infof("shutting down the FUSE server")

	if err := fuse.Unmount(fs.fuseMountPoint); err != nil {
		log.G(fs.ctx).WithField("fuse_mount_point", fs.fuseMountPoint).WithError(err).Error("failed to unmount the mountpoint")
		// Fallback: Lazy unmount ensures the mount point is detached even if busy,
		// preventing "transport endpoint not connected" errors for the caller.
		_ = syscall.Unmount(fs.fuseMountPoint, syscall.MNT_DETACH)
	}

	if err := fs.fuseConn.Close(); err != nil {
		log.G(fs.ctx).WithError(err).Error("failed to close the FUSE connection")
	}

	if *fuseDir == "" {
		if err := os.RemoveAll(*fuseDir); err != nil {
			log.G(fs.ctx).WithField("fuse_dir", *fuseDir).WithError(err).Error("failed to remove the FUSE dir")
		}
	}

	log.G(fs.ctx).Info("FUSE cleanup complete, signaling shutdown FIFO")
	if err := fs.fuseShutdownTrigger.Trigger(); err != nil {
		log.G(fs.ctx).WithError(err).Error("failed to trigger shutdown FIFO")
	}
}

func (fs fakeFUSE) Root() (fusefs.Node, error) {
	log.G(fs.ctx).Info("triggering ready via fifo")
	err := fs.fuseReadyTrigger.Trigger()
	if err != nil {
		log.G(fs.ctx).WithError(err).Fatalf("failed to trigger FUSE's readiness")
	}
	return &fuseFSRoot{attr: fs.attr}, nil
}

// This is required as the kernel stats the root directory.
func (fr *fuseFSRoot) Attr(ctx context.Context, a *fuse.Attr) error {
	if *hangDuringStat {
		log.G(ctx).Infof("will hang during Stat")
	} else {
		log.G(ctx).Infof("will not hang during Stat")
	}
	if err := fr.attr(ctx, a); err != nil {
		return err
	}
	a.Inode = 1 // Root inode
	a.Mode = os.ModeDir | 0o555
	return nil
}
