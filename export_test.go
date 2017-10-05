package containerd

import (
	"archive/tar"
	"io"
	"runtime"
	"testing"

	"github.com/containerd/containerd/images/oci"
)

// TestOCIExport exports testImage as a tar stream
func TestOCIExport(t *testing.T) {
	// TODO: support windows
	if testing.Short() || runtime.GOOS == "windows" {
		t.Skip()
	}
	ctx, cancel := testContext()
	defer cancel()

	client, err := New(address)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	pulled, err := client.Pull(ctx, testImage)
	if err != nil {
		t.Fatal(err)
	}
	exportedStream, err := client.Export(ctx, &oci.V1Exporter{}, pulled.Target())
	if err != nil {
		t.Fatal(err)
	}
	assertOCITar(t, exportedStream)
}

func assertOCITar(t *testing.T, r io.Reader) {
	// TODO: add more assertion
	tr := tar.NewReader(r)
	foundOCILayout := false
	foundIndexJSON := false
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Error(err)
			continue
		}
		if h.Name == "oci-layout" {
			foundOCILayout = true
		}
		if h.Name == "index.json" {
			foundIndexJSON = true
		}
	}
	if !foundOCILayout {
		t.Error("oci-layout not found")
	}
	if !foundIndexJSON {
		t.Error("index.json not found")
	}
}
