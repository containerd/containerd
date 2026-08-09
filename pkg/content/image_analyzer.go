package content

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/images"
	"github.com/containerd/containerd/platforms"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// ImageAnalyzer provides utilities for analyzing container images.
type ImageAnalyzer struct {
	store content.Store
}

// NewImageAnalyzer creates a new image analyzer.
func NewImageAnalyzer(store content.Store) *ImageAnalyzer {
	return &ImageAnalyzer{store: store}
}

// ImageInfo contains information about a container image.
type ImageInfo struct {
	MediaType    string
	Size         int64
	Digest       string
	Platforms    []string
	Layers       int
	CreatedAt    time.Time
	Labels       map[string]string
}

// AnalyzeImage analyzes a container image and returns its information.
func (a *ImageAnalyzer) AnalyzeImage(ctx context.Context, ref string) (*ImageInfo, error) {
	img, err := a.store.Get(ctx, ref)
	if err != nil {
		return nil, fmt.Errorf("failed to get image: %w", err)
	}

	info := &ImageInfo{
		MediaType: img.MediaType,
		Size:      img.Size,
		Digest:    img.Digest.String(),
		Labels:    img.Labels,
	}

	return info, nil
}

// GetImageSize returns the total size of an image.
func (a *ImageAnalyzer) GetImageSize(ctx context.Context, manifest *ocispec.Manifest) (int64, error) {
	var totalSize int64
	for _, layer := range manifest.Layers {
		totalSize += layer.Size
	}
	return totalSize, nil
}

// GetImagePlatforms returns the platforms an image supports.
func (a *ImageAnalyzer) GetImagePlatforms(ctx context.Context, desc *images.Descriptor) []string {
	var platforms []string
	if desc.Platform != nil {
		platforms = append(platforms, platforms.Format(*desc.Platform))
	}
	return platforms
}

// CompareImages compares two images and returns differences.
func (a *ImageAnalyzer) CompareImages(img1, img2 *ImageInfo) []string {
	var diffs []string

	if img1.Size != img2.Size {
		diffs = append(diffs, fmt.Sprintf("Size: %d vs %d", img1.Size, img2.Size))
	}

	if img1.Layers != img2.Layers {
		diffs = append(diffs, fmt.Sprintf("Layers: %d vs %d", img1.Layers, img2.Layers))
	}

	if img1.MediaType != img2.MediaType {
		diffs = append(diffs, fmt.Sprintf("MediaType: %s vs %s", img1.MediaType, img2.MediaType))
	}

	return diffs
}

// GetLayerInfo returns information about image layers.
func (a *ImageAnalyzer) GetLayerInfo(ctx context.Context, manifest *ocispec.Manifest) ([]LayerInfo, error) {
	var layers []LayerInfo
	for i, layer := range manifest.Layers {
		info := LayerInfo{
			Index:   i,
			Digest:  layer.Digest.String(),
			Size:    layer.Size,
			MediaType: layer.MediaType,
		}
		layers = append(layers, info)
	}
	return layers, nil
}

// LayerInfo contains information about an image layer.
type LayerInfo struct {
	Index     int
	Digest    string
	Size      int64
	MediaType string
}

// PrintImageReport prints a formatted report of image information.
func PrintImageReport(info *ImageInfo) string {
	report := fmt.Sprintf("Image Analysis Report\n")
	report += fmt.Sprintf("====================\n")
	report += fmt.Sprintf("Digest: %s\n", info.Digest)
	report += fmt.Sprintf("Media Type: %s\n", info.MediaType)
	report += fmt.Sprintf("Size: %d bytes\n", info.Size)
	report += fmt.Sprintf("Layers: %d\n", info.Layers)
	report += fmt.Sprintf("Platforms: %v\n", info.Platforms)
	report += fmt.Sprintf("Labels: %v\n", info.Labels)
	return report
}