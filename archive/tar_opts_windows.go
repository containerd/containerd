// +build windows

package archive

// ApplyOptions provides additional options for an Apply operation
type ApplyOptions struct {
	ParentLayerPaths        []string // Parent layer paths used for Windows layer apply
	IsWindowsContainerLayer bool     // True if the tar stream to be applied is a Windows Container Layer
}

// WithParentLayers adds parent layers to the apply process this is required
// for all Windows layers except the base layer.
func WithParentLayers(parentPaths []string) ApplyOpt {
	return func(options *ApplyOptions) error {
		options.ParentLayerPaths = parentPaths
		return nil
	}
}

// AsWindowsContainerLayer indicates that the tar stream to apply is that of
// a Windows Container Layer. The caller must be holding SeBackupPrivilege and
// SeRestorePrivilege.
func AsWindowsContainerLayer() ApplyOpt {
	return func(options *ApplyOptions) error {
		options.IsWindowsContainerLayer = true
		return nil
	}
}
