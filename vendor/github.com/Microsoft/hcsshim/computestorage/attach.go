//go:build windows

package computestorage

import (
	"context"
	"encoding/json"

	"github.com/Microsoft/hcsshim/internal/oc"
	"github.com/pkg/errors"
	"go.opencensus.io/trace"
)

// AttachLayerStorageFilter sets up the layer storage filter on a writable
// container layer.
//
// `layerPath` is a path to a directory the writable layer is mounted. If the
// path does not end in a `\` the platform will append it automatically.
//
// `layerData` is the parent read-only layer data.
func AttachLayerStorageFilter(ctx context.Context, layerPath string, layerData LayerData) (err error) {
	title := "hcsshim::AttachLayerStorageFilter"
	ctx, span := oc.StartSpan(ctx, title) //nolint:ineffassign,staticcheck
	defer span.End()
	defer func() { oc.SetSpanStatus(span, err) }()
	span.AddAttributes(
		trace.StringAttribute("layerPath", layerPath),
	)

	bytes, err := json.Marshal(layerData)
	if err != nil {
		return err
	}

	err = hcsAttachLayerStorageFilter(layerPath, string(bytes))
	if err != nil {
		return errors.Wrap(err, "failed to attach layer storage filter")
	}
	return nil
}

// AttachUnionFSFilter sets up unionfs on a writable container layer.
//
// `volumePath` is volume path at which writable layer is mounted. If the
// path does not end in a `\` the platform will append it automatically.
//
// `layerData` is the parent read-only layer data.
func AttachUnionFSFilter(ctx context.Context, volumePath string, layerData LayerData) (err error) {
	title := "hcsshim::AttachUnionFSFilter"
	ctx, span := oc.StartSpan(ctx, title) //nolint:ineffassign,staticcheck
	defer span.End()
	defer func() { oc.SetSpanStatus(span, err) }()
	span.AddAttributes(
		trace.StringAttribute("volumePath", volumePath),
	)

	bytes, err := json.Marshal(layerData)
	if err != nil {
		return err
	}

	err = hcsAttachOverlayFilter(volumePath, string(bytes))
	if err != nil {
		return errors.Wrap(err, "failed to attach unionfs filter")
	}
	return nil
}
