package containerd

import (
	"io"
	"os"
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

func TestOCILocalExportAndImport(t *testing.T) {
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

	imageTar, err := os.OpenFile("temp.tar", os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0666)
	if err != nil {
		t.Fatal(err)
	}
	defer imageTar.Close()
	defer os.Remove("temp.tar")

	buf := make([]byte, 4096)
	for {
		r, err := exported.Read(buf)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		_, err = imageTar.Write(buf[:r])
		if err != nil {
			t.Fatal(err)
		}
	}

	file, err := os.OpenFile("temp.tar", os.O_RDONLY, 0666)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	imported, err := client.Import(ctx, &oci.V1Importer{ImageName: "foo/bar:"}, file)
	if err != nil {
		t.Fatal(err)
	}

	for _, img := range imported {
		err = client.ImageService().Delete(ctx, img.Name())
		if err != nil {
			t.Fatal(err)
		}
	}

}
