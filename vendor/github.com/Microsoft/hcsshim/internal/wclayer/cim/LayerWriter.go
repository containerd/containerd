//go:build windows

package cim

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/Microsoft/go-winio"
	"github.com/Microsoft/hcsshim/internal/oc"
	"github.com/Microsoft/hcsshim/internal/wclayer"
	"github.com/Microsoft/hcsshim/pkg/cimfs"
	"go.opencensus.io/trace"
)

// A CimLayerWriter implements the wclayer.LayerWriter interface to allow writing container
// image layers in the cim format.
// A cim layer consist of cim files (which are usually stored in the `cim-layers` directory and
// some other files which are stored in the directory of that layer (i.e the `path` directory).
type CimLayerWriter struct {
	ctx context.Context
	s   *trace.Span
	// path to the layer (i.e layer's directory) as provided by the caller.
	// Even if a layer is stored as a cim in the cim directory, some files associated
	// with a layer are still stored in this path.
	layerPath string
	// parent layer paths
	parentLayerPaths []string
	// Handle to the layer cim - writes to the cim file
	cimWriter *cimfs.CimFsWriter
	// Handle to the writer for writing files in the local filesystem
	stdFileWriter *stdFileWriter
	// reference to currently active writer either cimWriter or stdFileWriter
	activeWriter io.Writer
	// denotes if this layer has the UtilityVM directory
	hasUtilityVM bool
	// some files are written outside the cim during initial import (via stdFileWriter) because we need to
	// make some modifications to these files before writing them to the cim. The pendingOps slice
	// maintains a list of such delayed modifications to the layer cim. These modifications are applied at
	// the very end of layer import process.
	pendingOps []pendingCimOp
}

type hive struct {
	name  string
	base  string
	delta string
}

var (
	hives = []hive{
		{"SYSTEM", "SYSTEM_BASE", "SYSTEM_DELTA"},
		{"SOFTWARE", "SOFTWARE_BASE", "SOFTWARE_DELTA"},
		{"SAM", "SAM_BASE", "SAM_DELTA"},
		{"SECURITY", "SECURITY_BASE", "SECURITY_DELTA"},
		{"DEFAULT", "DEFAULTUSER_BASE", "DEFAULTUSER_DELTA"},
	}
)

func isDeltaOrBaseHive(path string) bool {
	for _, hv := range hives {
		if strings.EqualFold(path, filepath.Join(wclayer.HivesPath, hv.delta)) ||
			strings.EqualFold(path, filepath.Join(wclayer.RegFilesPath, hv.name)) {
			return true
		}
	}
	return false
}

// checks if this particular file should be written with a stdFileWriter instead of
// using the cimWriter.
func isStdFile(path string) bool {
	return (isDeltaOrBaseHive(path) ||
		path == filepath.Join(wclayer.UtilityVMPath, wclayer.RegFilesPath, "SYSTEM") ||
		path == filepath.Join(wclayer.UtilityVMPath, wclayer.RegFilesPath, "SOFTWARE") ||
		path == wclayer.BcdFilePath || path == wclayer.BootMgrFilePath)
}

// Add adds a file to the layer with given metadata.
func (cw *CimLayerWriter) Add(name string, fileInfo *winio.FileBasicInfo, fileSize int64, securityDescriptor []byte, extendedAttributes []byte, reparseData []byte) error {
	if name == wclayer.UtilityVMPath {
		cw.hasUtilityVM = true
	}
	if isStdFile(name) {
		// create a pending op for this file
		cw.pendingOps = append(cw.pendingOps, &addOp{
			pathInCim:          name,
			hostPath:           filepath.Join(cw.layerPath, name),
			fileInfo:           fileInfo,
			securityDescriptor: securityDescriptor,
			extendedAttributes: extendedAttributes,
			reparseData:        reparseData,
		})
		if err := cw.stdFileWriter.Add(name); err != nil {
			return err
		}
		cw.activeWriter = cw.stdFileWriter
	} else {
		if err := cw.cimWriter.AddFile(name, fileInfo, fileSize, securityDescriptor, extendedAttributes, reparseData); err != nil {
			return err
		}
		cw.activeWriter = cw.cimWriter
	}
	return nil
}

// AddLink adds a hard link to the layer. The target must already have been added.
func (cw *CimLayerWriter) AddLink(name string, target string) error {
	// set active write to nil so that we panic if layer tar is incorrectly formatted.
	cw.activeWriter = nil
	if isStdFile(target) {
		// If this is a link to a std file it will have to be added later once the
		// std file is written to the CIM. Create a pending op for this
		cw.pendingOps = append(cw.pendingOps, &linkOp{
			oldPath: target,
			newPath: name,
		})
		return nil
	} else if isStdFile(name) {
		// None of the predefined std files are links. If they show up as links this is unexpected
		// behavior. Error out.
		return fmt.Errorf("unexpected link %s in layer", name)
	} else {
		return cw.cimWriter.AddLink(target, name)
	}
}

// AddAlternateStream creates another alternate stream at the given
// path. Any writes made after this call will go to that stream.
func (cw *CimLayerWriter) AddAlternateStream(name string, size uint64) error {
	if isStdFile(name) {
		// As of now there is no known case of std file having multiple data streams.
		// If such a file is encountered our assumptions are wrong. Error out.
		return fmt.Errorf("unexpected alternate stream %s in layer", name)
	}

	if err := cw.cimWriter.CreateAlternateStream(name, size); err != nil {
		return err
	}
	cw.activeWriter = cw.cimWriter
	return nil
}

// Remove removes a file that was present in a parent layer from the layer.
func (cw *CimLayerWriter) Remove(name string) error {
	// set active write to nil so that we panic if layer tar is incorrectly formatted.
	cw.activeWriter = nil
	return cw.cimWriter.Unlink(name)
}

// Write writes data to the current file. The data must be in the format of a Win32
// backup stream.
func (cw *CimLayerWriter) Write(b []byte) (int, error) {
	return cw.activeWriter.Write(b)
}

// Close finishes the layer writing process and releases any resources.
func (cw *CimLayerWriter) Close(ctx context.Context) (retErr error) {
	if err := cw.stdFileWriter.Close(ctx); err != nil {
		return err
	}

	// cimWriter must be closed even if there are errors.
	defer func() {
		if err := cw.cimWriter.Close(); retErr == nil {
			retErr = err
		}
	}()

	// UVM based containers aren't supported with CimFS, don't process the UVM layer
	processUtilityVM := false

	if len(cw.parentLayerPaths) == 0 {
		if err := cw.processBaseLayer(ctx, processUtilityVM); err != nil {
			return fmt.Errorf("process base layer: %w", err)
		}
	} else {
		if err := cw.processNonBaseLayer(ctx, processUtilityVM); err != nil {
			return fmt.Errorf("process non base layer: %w", err)
		}
	}

	for _, op := range cw.pendingOps {
		if err := op.apply(cw.cimWriter); err != nil {
			return fmt.Errorf("apply pending operations: %w", err)
		}
	}
	return nil
}

func NewCimLayerWriter(ctx context.Context, layerPath, cimPath string, parentLayerPaths, parentLayerCimPaths []string) (_ *CimLayerWriter, err error) {
	if !cimfs.IsCimFSSupported() {
		return nil, fmt.Errorf("CimFs not supported on this build")
	}

	ctx, span := trace.StartSpan(ctx, "hcsshim::NewCimLayerWriter")
	defer func() {
		if err != nil {
			oc.SetSpanStatus(span, err)
			span.End()
		}
	}()
	span.AddAttributes(
		trace.StringAttribute("path", layerPath),
		trace.StringAttribute("cimPath", cimPath),
		trace.StringAttribute("parentLayerPaths", strings.Join(parentLayerCimPaths, ", ")),
		trace.StringAttribute("parentLayerPaths", strings.Join(parentLayerPaths, ", ")))

	parentCim := ""
	if len(parentLayerPaths) > 0 {
		if filepath.Dir(cimPath) != filepath.Dir(parentLayerCimPaths[0]) {
			return nil, fmt.Errorf("parent cim can not be stored in different directory")
		}
		// We only need to provide parent CIM name, it is assumed that both parent CIM
		// and newly created CIM are present in the same directory.
		parentCim = filepath.Base(parentLayerCimPaths[0])
	}

	cim, err := cimfs.Create(filepath.Dir(cimPath), parentCim, filepath.Base(cimPath))
	if err != nil {
		return nil, fmt.Errorf("error in creating a new cim: %w", err)
	}

	sfw, err := newStdFileWriter(layerPath, parentLayerPaths)
	if err != nil {
		return nil, fmt.Errorf("error in creating new standard file writer: %w", err)
	}
	return &CimLayerWriter{
		ctx:              ctx,
		s:                span,
		layerPath:        layerPath,
		parentLayerPaths: parentLayerPaths,
		cimWriter:        cim,
		stdFileWriter:    sfw,
	}, nil
}
