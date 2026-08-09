package content

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/containerd/containerd/content"
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
	MediaType string
	Size      int64
	Digest    string
	Platforms []string
	Layers    int
	CreatedAt time.Time
	Labels    map[string]string
}

// AnalyzeImage analyzes a container image by reading its descriptor from the content store.
func (a *ImageAnalyzer) AnalyzeImage(ctx context.Context, desc ocispec.Descriptor) (*ImageInfo, error) {
	reader, err := a.store.ReaderAt(ctx, desc)
	if err != nil {
		return nil, fmt.Errorf("failed to read content: %w", err)
	}
	defer reader.Close()

	platformsList := []string{}
	if desc.Platform != nil {
		platformsList = append(platformsList, platforms.Format(*desc.Platform))
	}

	info := &ImageInfo{
		MediaType: desc.MediaType,
		Size:      desc.Size,
		Digest:    desc.Digest.String(),
		Platforms: platformsList,
		Labels:    desc.Annotations,
	}

	return info, nil
}

// GetImageSize returns the total size of an image manifest.
func (a *ImageAnalyzer) GetImageSize(manifest *ocispec.Manifest) (int64, error) {
	var totalSize int64
	for _, layer := range manifest.Layers {
		totalSize += layer.Size
	}
	return totalSize, nil
}

// GetImagePlatforms returns the platforms an image supports.
func (a *ImageAnalyzer) GetImagePlatforms(desc *ocispec.Descriptor) []string {
	var platformsList []string
	if desc.Platform != nil {
		platformsList = append(platformsList, platforms.Format(*desc.Platform))
	}
	return platformsList
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

// GetLayerInfo returns information about image layers from a manifest.
func (a *ImageAnalyzer) GetLayerInfo(manifest *ocispec.Manifest) []LayerInfo {
	var layers []LayerInfo
	for i, layer := range manifest.Layers {
		info := LayerInfo{
			Index:     i,
			Digest:    layer.Digest.String(),
			Size:      layer.Size,
			MediaType: layer.MediaType,
		}
		layers = append(layers, info)
	}
	return layers
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
	report := "Image Analysis Report\n"
	report += "====================\n"
	report += fmt.Sprintf("Digest: %s\n", info.Digest)
	report += fmt.Sprintf("Media Type: %s\n", info.MediaType)
	report += fmt.Sprintf("Size: %d bytes\n", info.Size)
	report += fmt.Sprintf("Layers: %d\n", info.Layers)
	report += fmt.Sprintf("Platforms: %v\n", info.Platforms)
	report += fmt.Sprintf("Labels: %v\n", info.Labels)
	return report
}