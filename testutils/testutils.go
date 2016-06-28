package testutils

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"encoding/json"
	"io/ioutil"

	"github.com/opencontainers/runtime-spec/specs-go"
)

// GetTestOutDir returns the output directory for testing and benchmark artifacts
func GetTestOutDir() string {
	out, _ := exec.Command("git", "rev-parse", "--show-toplevel").CombinedOutput()
	repoRoot := string(out)
	prefix := filepath.Join(strings.TrimSpace(repoRoot), "output")
	return prefix
}

var (
	// ArchivesDir holds the location of the available rootfs
	ArchivesDir = filepath.Join("test-artifacts", "archives")
	// BundlesRoot holds the location where OCI Bundles are stored
	BundlesRoot = filepath.Join("test-artifacts", "oci-bundles")
	// OutputDirFormat holds the standard format used when creating a
	// new test output directory
	OutputDirFormat = filepath.Join("test-artifacts", "runs", "%s")
	// RefOciSpecsPath holds the path to the generic OCI config
	RefOciSpecsPath = filepath.Join(BundlesRoot, "config.json")
	// StateDir holds the path to the directory used by the containerd
	// started by tests
	StateDir = "/run/containerd-bench-test"
)

// untarRootfs untars the given `source` tarPath into `destination/rootfs`
func untarRootfs(source string, destination string) error {
	rootfs := filepath.Join(destination, "rootfs")

	if err := os.MkdirAll(rootfs, 0755); err != nil {
		fmt.Println("untarRootfs os.MkdirAll failed with err %v", err)
		return nil
	}
	tar := exec.Command("tar", "-C", rootfs, "-xf", source)
	return tar.Run()
}

// GenerateReferenceSpecs generates a OCI spec via `runc spec` and change its process args
func GenerateReferenceSpecs(destination string, processArgs []string) error {
	specFilePath := filepath.Join(destination, "config.json");
	if _, err := os.Stat(specFilePath); err == nil {
		return nil
	}
	specCommand := exec.Command("runc", "spec")
	specCommand.Dir = destination
	err := specCommand.Run()
	if err != nil {
		return err
	}
	// open spec file
	specFile, err := os.OpenFile(specFilePath, os.O_RDWR, 755)
	if err != nil {
		return err
	}
	defer specFile.Close()
	// read spec file content
	specContent, err := ioutil.ReadAll(specFile)
	if err != nil {
		return nil
	}
	// Unmarshal json data
	var spec specs.Spec
	err = json.Unmarshal(specContent, &spec)
	if err != nil {
		return err
	}
	// copy new process args
	spec.Process.Args=processArgs
	//save spec
	specFile.Seek(0, 0)
	specFile.Truncate(0)
	err = json.NewEncoder(specFile).Encode(&spec)
	if err != nil {
		return err
	}
	return nil
}

// CreateBundle generates a valid OCI bundle from the given rootfs
func CreateBundle(source, name string) error {
	bundlePath := filepath.Join(BundlesRoot, name)

	if err := untarRootfs(filepath.Join(ArchivesDir, source+".tar"), bundlePath); err != nil {
		return fmt.Errorf("Failed to untar %s.tar: %v", source, err)
	}

	return nil
}

// CreateBusyboxBundle generates a bundle based on the busybox rootfs
func CreateBusyboxBundle(name string) error {
	return CreateBundle("busybox", name)
}
