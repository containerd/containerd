package content

import (
	"testing"
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
	layers := []LayerInfo{
		{Index: 0, Digest: "sha256:layer1", Size: 100, MediaType: "application/gzip"},
		{Index: 1, Digest: "sha256:layer2", Size: 200, MediaType: "application/gzip"},
	}

	if len(layers) != 2 {
		t.Errorf("Expected 2 layers, got %d", len(layers))
	}
}