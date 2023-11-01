//go:build gofuzz

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

package client

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	fuzz "github.com/AdaLogics/go-fuzz-headers"
	"github.com/containerd/containerd/v2"
	"github.com/containerd/containerd/v2/oci"
	exec "golang.org/x/sys/execabs"
)

var (
	haveDownloadedbinaries = false
	haveExtractedBinaries  = false
	haveChangedPATH        = false
	haveInitialized        = false

	downloadLink = "https://github.com/containerd/containerd/releases/download/v1.5.4/containerd-1.5.4-linux-amd64.tar.gz"
	downloadPath = "/tmp/containerd-1.5.4-linux-amd64.tar.gz"
	binariesDir  = "/tmp/containerd-binaries"
)

// downloadFile downloads a file from a url
func downloadFile(filepath string, url string) (err error) {

	out, err := os.Create(filepath)
	if err != nil {
		return err
	}
	defer out.Close()

	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	_, err = io.Copy(out, resp.Body)
	if err != nil {
		return err
	}

	return nil
}

// initInSteps() performs initialization in several steps
// The reason for spreading the initialization out in
// multiple steps is that each fuzz iteration can maximum
// take 25 seconds when running through OSS-fuzz.
// Should an iteration exceed that, then the fuzzer stops.
func initInSteps() bool {
	// Download binaries
	if !haveDownloadedbinaries {
		err := downloadFile(downloadPath, downloadLink)
		if err != nil {
			panic(err)
		}
		haveDownloadedbinaries = true
	}
	// Extract binaries
	if !haveExtractedBinaries {
		err := os.MkdirAll(binariesDir, 0777)
		if err != nil {
			return true
		}
		cmd := exec.Command("tar", "xvf", downloadPath, "-C", binariesDir)
		err = cmd.Run()
		if err != nil {
			return true
		}
		haveExtractedBinaries = true
		return true
	}
	// Add binaries to $PATH:
	if !haveChangedPATH {
		oldPathEnv := os.Getenv("PATH")
		newPathEnv := fmt.Sprintf("%s/bin:%s", binariesDir, oldPathEnv)
		err := os.Setenv("PATH", newPathEnv)
		if err != nil {
			return true
		}
		haveChangedPATH = true
		return true
	}
	haveInitialized = true
	return false
}

func tearDown() error {
	if err := ctrd.Stop(); err != nil {
		if err := ctrd.Kill(); err != nil {
			return err
		}
	}
	if err := ctrd.Wait(); err != nil {
		if _, ok := err.(*exec.ExitError); !ok {
			return err
		}
	}
	if err := forceRemoveAll(defaultRoot); err != nil {
		return err
	}

	return nil
}

// checkIfShouldRestart() checks if an error indicates that
// the daemon is not running. If the daemon is not running,
// it deletes it to allow the fuzzer to create a new and
// working socket.
func checkIfShouldRestart(err error) {
	if strings.Contains(err.Error(), "daemon is not running") {
		deleteSocket()
	}
}

// startDaemon() starts the daemon.
func startDaemon(ctx context.Context, shouldTearDown bool) {
	buf := bytes.NewBuffer(nil)
	stdioFile, err := os.CreateTemp("", "")
	if err != nil {
		// We panic here as it is a fuzz-blocker that
		// may need fixing
		panic(err)
	}
	defer func() {
		stdioFile.Close()
		os.Remove(stdioFile.Name())
	}()
	ctrdStdioFilePath = stdioFile.Name()
	stdioWriter := io.MultiWriter(stdioFile, buf)
	err = ctrd.start("containerd", address, []string{
		"--root", defaultRoot,
		"--state", defaultState,
		"--log-level", "debug",
		"--config", createShimDebugConfig(),
	}, stdioWriter, stdioWriter)
	if err != nil {
		// We are fine if the error is that the daemon is already running,
		// but if the error is another, then it will be a fuzz blocker,
		// so we panic
		if !strings.Contains(err.Error(), "daemon is already running") {
			fmt.Fprintf(os.Stderr, "%s: %s\n", err, buf.String())
		}
	}
	if shouldTearDown {
		defer func() {
			err = tearDown()
			if err != nil {
				checkIfShouldRestart(err)
			}
		}()
	}
	seconds := 4 * time.Second
	waitCtx, waitCancel := context.WithTimeout(ctx, seconds)

	_, err = ctrd.waitForStart(waitCtx)
	waitCancel()
	if err != nil {
		ctrd.Stop()
		ctrd.Kill()
		ctrd.Wait()
		fmt.Fprintf(os.Stderr, "%s: %s\n", err, buf.String())
		return
	}
}

// deleteSocket() deletes the socket in the file system.
// This is needed because the socket occasionally will
// refuse a connection to it, and deleting it allows us
// to create a new socket when invoking containerd.New()
func deleteSocket() error {
	err := os.Remove(defaultAddress)
	if err != nil {
		return err
	}
	return nil
}

// updatePathEnv() creates an empty directory in which
// the fuzzer will create the containerd socket.
// updatePathEnv() furthermore adds "/out/containerd-binaries"
// to $PATH, since the binaries are available there.
func updatePathEnv() error {
	// Create test dir for socket
	err := os.MkdirAll(defaultState, 0777)
	if err != nil {
		return err
	}

	oldPathEnv := os.Getenv("PATH")
	newPathEnv := oldPathEnv + ":/out/containerd-binaries"
	err = os.Setenv("PATH", newPathEnv)
	if err != nil {
		return err
	}
	haveInitialized = true
	return nil
}

// checkAndDoUnpack checks if an image is unpacked.
// If it is not unpacked, then we may or may not
// unpack it. The fuzzer decides.
func checkAndDoUnpack(image containerd.Image, ctx context.Context, f *fuzz.ConsumeFuzzer) {
	unpacked, err := image.IsUnpacked(ctx, testSnapshotter)
	if err == nil && unpacked {
		shouldUnpack, err := f.GetBool()
		if err == nil && shouldUnpack {
			_ = image.Unpack(ctx, testSnapshotter)
		}
	}
}

// getImage() returns an image from the client.
// The fuzzer decides which image is returned.
func getImage(client *containerd.Client, f *fuzz.ConsumeFuzzer) (containerd.Image, error) {
	images, err := client.ListImages(nil)
	if err != nil {
		return nil, err
	}
	imageIndex, err := f.GetInt()
	if err != nil {
		return nil, err
	}
	image := images[imageIndex%len(images)]
	return image, nil

}

// newContainer creates and returns a container
// The fuzzer decides how the container is created
func newContainer(client *containerd.Client, f *fuzz.ConsumeFuzzer, ctx context.Context) (containerd.Container, error) {

	// determiner determines how we should create the container
	determiner, err := f.GetInt()
	if err != nil {
		return nil, err
	}
	id, err := f.GetString()
	if err != nil {
		return nil, err
	}

	if determiner%1 == 0 {
		// Create a container with oci specs
		spec := &oci.Spec{}
		err = f.GenerateStruct(spec)
		if err != nil {
			return nil, err
		}
		container, err := client.NewContainer(ctx, id,
			containerd.WithSpec(spec))
		if err != nil {
			return nil, err
		}
		return container, nil
	} else if determiner%2 == 0 {
		// Create a container with fuzzed oci specs
		// and an image
		image, err := getImage(client, f)
		if err != nil {
			return nil, err
		}
		// Fuzz a few image APIs
		_, _ = image.Size(ctx)
		checkAndDoUnpack(image, ctx, f)

		spec := &oci.Spec{}
		err = f.GenerateStruct(spec)
		if err != nil {
			return nil, err
		}
		container, err := client.NewContainer(ctx,
			id,
			containerd.WithImage(image),
			containerd.WithSpec(spec))
		if err != nil {
			return nil, err
		}
		return container, nil
	} else {
		// Create a container with an image
		image, err := getImage(client, f)
		if err != nil {
			return nil, err
		}
		// Fuzz a few image APIs
		_, _ = image.Size(ctx)
		checkAndDoUnpack(image, ctx, f)

		container, err := client.NewContainer(ctx,
			id,
			containerd.WithImage(image))
		if err != nil {
			return nil, err
		}
		return container, nil
	}
	return nil, errors.New("Could not create container")
}

// doFuzz() implements the logic of FuzzIntegCreateContainerNoTearDown()
// and FuzzIntegCreateContainerWithTearDown() and allows for
// the option to turn on/off teardown after each iteration.
// From a high level it:
// - Creates a client
// - Imports a bunch of fuzzed tar archives
// - Creates a bunch of containers
func doFuzz(data []byte, shouldTearDown bool) int {
	ctx, cancel := testContext(nil)
	defer cancel()

	// Check if daemon is running and start it if it isn't
	if ctrd.cmd == nil {
		startDaemon(ctx, shouldTearDown)
	}
	client, err := containerd.New(defaultAddress)
	if err != nil {
		// The error here is most likely with the socket.
		// Deleting it will allow the creation of a new
		// socket during next fuzz iteration.
		deleteSocket()
		return -1
	}
	defer client.Close()

	f := fuzz.NewConsumer(data)

	// Begin import tars:
	noOfImports, err := f.GetInt()
	if err != nil {
		return 0
	}
	// maxImports is currently completely arbitrarily defined
	maxImports := 30
	for i := 0; i < noOfImports%maxImports; i++ {

		// f.TarBytes() returns valid tar bytes.
		tarBytes, err := f.TarBytes()
		if err != nil {
			return 0
		}
		_, _ = client.Import(ctx, bytes.NewReader(tarBytes))
	}
	// End import tars

	// Begin create containers:
	existingImages, err := client.ListImages(ctx)
	if err != nil {
		return 0
	}
	if len(existingImages) > 0 {
		noOfContainers, err := f.GetInt()
		if err != nil {
			return 0
		}
		// maxNoOfContainers is currently
		// completely arbitrarily defined
		maxNoOfContainers := 50
		for i := 0; i < noOfContainers%maxNoOfContainers; i++ {
			container, err := newContainer(client, f, ctx)
			if err == nil {
				defer container.Delete(ctx, containerd.WithSnapshotCleanup)
			}
		}
	}
	// End create containers

	return 1
}

// FuzzIntegNoTearDownWithDownload() implements a fuzzer
// similar to FuzzIntegCreateContainerNoTearDown() and
// FuzzIntegCreateContainerWithTearDown(), but it takes a
// different approach to the initialization. Where
// the other 2 fuzzers depend on the containerd binaries
// that were built manually, this fuzzer downloads them
// when starting a fuzz run.
// This fuzzer is experimental for now and is being run
// continuously by OSS-fuzz to collect feedback on
// its sustainability.
func FuzzIntegNoTearDownWithDownload(data []byte) int {
	if !haveInitialized {
		shouldRestart := initInSteps()
		if shouldRestart {
			return 0
		}
	}
	ret := doFuzz(data, false)
	return ret
}

// FuzzIntegCreateContainerNoTearDown() implements a fuzzer
// similar to FuzzIntegCreateContainerWithTearDown() with
// with one minor distinction: One tears down the
// daemon after each iteration whereas the other doesn't.
// The two fuzzers' performance will be compared over time.
func FuzzIntegCreateContainerNoTearDown(data []byte) int {
	if !haveInitialized {
		err := updatePathEnv()
		if err != nil {
			return 0
		}
	}
	ret := doFuzz(data, false)
	return ret
}

// FuzzIntegCreateContainerWithTearDown() is similar to
// FuzzIntegCreateContainerNoTearDown() except that
// FuzzIntegCreateContainerWithTearDown tears down the daemon
// after each iteration.
func FuzzIntegCreateContainerWithTearDown(data []byte) int {
	if !haveInitialized {
		err := updatePathEnv()
		if err != nil {
			return 0
		}
	}
	ret := doFuzz(data, true)
	return ret
}
