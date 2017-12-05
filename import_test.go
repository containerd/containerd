package containerd

import (
	"runtime"
	"testing"

	"github.com/containerd/containerd/images/oci"
)

// TestOCIExportAndImport exports testImage as a tar stream,
// and import the tar stream as a new image.
func TestOCIExportAndImport(t *testing.T) {
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

	exported, err := client.Export(ctx, &oci.V1Exporter{}, pulled.Target())
	if err != nil {
		t.Fatal(err)
	}

	imgrecs, err := client.Import(ctx, &oci.V1Importer{ImageName: "foo/bar:"}, exported)
	if err != nil {
		t.Fatal(err)
	}

	for _, imgrec := range imgrecs {
		err = client.ImageService().Delete(ctx, imgrec.Name())
		if err != nil {
			t.Fatal(err)
		}
	}
}
