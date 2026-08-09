package content

import (
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/opencontainers/go-digest"
)

func TestCompareImages_SameSize(t *testing.T) {
	img1 := &ImageInfo{Size: 100, Layers: 5, MediaType: "application/json"}
	img2 := &ImageInfo{Size: 100, Layers: 5, MediaType: "application/json"}

	analyzer := &ImageAnalyzer{}
	diffs := analyzer.CompareImages(img1, img2)

	if len(diffs) != 0 {
		t.Errorf("Expected no differences, got %d", len(diffs))
	}
}

func TestCompareImages_DifferentSize(t *testing.T) {
	img1 := &ImageInfo{Size: 100, Layers: 5, MediaType: "application/json"}
	img2 := &ImageInfo{Size: 200, Layers: 5, MediaType: "application/json"}

	analyzer := &ImageAnalyzer{}
	diffs := analyzer.CompareImages(img1, img2)

	if len(diffs) != 1 {
		t.Errorf("Expected 1 difference, got %d", len(diffs))
	}
}

func TestCompareImages_DifferentLayers(t *testing.T) {
	img1 := &ImageInfo{Size: 100, Layers: 5, MediaType: "application/json"}
	img2 := &ImageInfo{Size: 100, Layers: 10, MediaType: "application/json"}

	analyzer := &ImageAnalyzer{}
	diffs := analyzer.CompareImages(img1, img2)

	if len(diffs) != 1 {
		t.Errorf("Expected 1 difference, got %d", len(diffs))
	}
}

func TestCompareImages_MultipleDiffs(t *testing.T) {
	img1 := &ImageInfo{Size: 100, Layers: 5, MediaType: "application/json"}
	img2 := &ImageInfo{Size: 200, Layers: 10, MediaType: "application/xml"}

	analyzer := &ImageAnalyzer{}
	diffs := analyzer.CompareImages(img1, img2)

	if len(diffs) != 3 {
		t.Errorf("Expected 3 differences, got %d", len(diffs))
	}
}

func TestPrintImageReport(t *testing.T) {
	info := &ImageInfo{
		Digest:    "sha256:abc123",
		MediaType: "application/json",
		Size:      12345,
		Layers:    5,
		Platforms: []string{"linux/amd64"},
		Labels:    map[string]string{"version": "1.0"},
	}

	report := PrintImageReport(info)

	if report == "" {
		t.Error("Expected non-empty report")
	}
}

func TestGetLayerInfo(t *testing.T) {
	analyzer := &ImageAnalyzer{}

	manifest := &ocispec.Manifest{
		Layers: []ocispec.Descriptor{
			{
				Digest:    digest.FromString("layer1"),
				Size:      100,
				MediaType: "application/gzip",
			},
			{
				Digest:    digest.FromString("layer2"),
				Size:      200,
				MediaType: "application/gzip",
			},
		},
	}

	layers := analyzer.GetLayerInfo(manifest)

	if len(layers) != 2 {
		t.Errorf("Expected 2 layers, got %d", len(layers))
	}
	if layers[0].Index != 0 {
		t.Errorf("Expected index 0, got %d", layers[0].Index)
	}
	if layers[1].Size != 200 {
		t.Errorf("Expected size 200, got %d", layers[1].Size)
	}
}

func TestGetImagePlatforms(t *testing.T) {
	analyzer := &ImageAnalyzer{}

	arch := "amd64"
	os := "linux"
	desc := &ocispec.Descriptor{
		MediaType: "application/json",
		Platform: &ocispec.Platform{
			Architecture: arch,
			OS:           os,
		},
	}

	platforms := analyzer.GetImagePlatforms(desc)

	if len(platforms) != 1 {
		t.Errorf("Expected 1 platform, got %d", len(platforms))
	}
}

func TestGetImagePlatforms_Nil(t *testing.T) {
	analyzer := &ImageAnalyzer{}

	desc := &ocispec.Descriptor{
		MediaType: "application/json",
	}

	platforms := analyzer.GetImagePlatforms(desc)

	if len(platforms) != 0 {
		t.Errorf("Expected 0 platforms, got %d", len(platforms))
	}
}

func TestGetImageSize(t *testing.T) {
	analyzer := &ImageAnalyzer{}

	manifest := &ocispec.Manifest{
		Layers: []ocispec.Descriptor{
			{Size: 100},
			{Size: 200},
		},
	}

	size, err := analyzer.GetImageSize(manifest)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if size != 300 {
		t.Errorf("Expected 300, got %d", size)
	}
}