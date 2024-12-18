//go:build windows
// +build windows

package cim

import (
	"archive/tar"
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/Microsoft/go-winio/backuptar"
	"github.com/Microsoft/hcsshim/internal/log"
	"github.com/Microsoft/hcsshim/internal/wclayer/cim"
	"github.com/Microsoft/hcsshim/pkg/cimfs"
	"github.com/Microsoft/hcsshim/pkg/ociwclayer"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/windows"
)

// ImportCimLayerFromTar reads a layer from an OCI layer tar stream and extracts it into
// the CIM format at the specified path.
// `layerPath` is the directory which can be used to store intermediate files generated during layer extraction (and these file are also used when extracting children layers of this layer)
// `cimPath` is the path to the CIM in which layer files must be stored. Note that region & object files are created when writing to a CIM, these files will be created next to the `cimPath`.
// `parentLayerCimPaths` are paths to the parent layer CIMs, ordered from highest to lowest, i.e the CIM at `parentLayerCimPaths[0]` will be the immediate parent of the layer that is being extracted here.
// `parentLayerPaths` are paths to the parent layer directories. Ordered from highest to lowest.
//
// This function returns the total size of the layer's files, in bytes.
func ImportCimLayerFromTar(ctx context.Context, r io.Reader, layerPath, cimPath string, parentLayerPaths, parentLayerCimPaths []string) (_ int64, err error) {
	log.G(ctx).WithFields(logrus.Fields{
		"layer path":             layerPath,
		"layer cim path":         cimPath,
		"parent layer paths":     strings.Join(parentLayerPaths, ", "),
		"parent layer CIM paths": strings.Join(parentLayerCimPaths, ", "),
	}).Debug("Importing cim layer from tar")

	err = os.MkdirAll(layerPath, 0)
	if err != nil {
		return 0, err
	}

	w, err := cim.NewForkedCimLayerWriter(ctx, layerPath, cimPath, parentLayerPaths, parentLayerCimPaths)
	if err != nil {
		return 0, err
	}

	n, err := writeCimLayerFromTar(ctx, r, w)
	cerr := w.Close(ctx)
	if err != nil {
		return 0, err
	}
	if cerr != nil {
		return 0, cerr
	}
	return n, nil
}

// ImportSingleFileCimLayerFromTar reads a layer from an OCI layer tar stream and extracts
// it into the SingleFileCIM format.
func ImportSingleFileCimLayerFromTar(ctx context.Context, r io.Reader, layer *cimfs.BlockCIM, parentLayers []*cimfs.BlockCIM) (_ int64, err error) {
	log.G(ctx).WithFields(logrus.Fields{
		"layer":         layer,
		"parent layers": fmt.Sprintf("%v", parentLayers),
	}).Debug("Importing single file cim layer from tar")

	err = os.MkdirAll(filepath.Dir(layer.BlockPath), 0)
	if err != nil {
		return 0, err
	}

	w, err := cim.NewBlockCIMLayerWriter(ctx, layer, parentLayers)
	if err != nil {
		return 0, err
	}

	n, err := writeCimLayerFromTar(ctx, r, w)
	cerr := w.Close(ctx)
	if err != nil {
		return 0, err
	}
	if cerr != nil {
		return 0, cerr
	}
	return n, nil
}

func writeCimLayerFromTar(ctx context.Context, r io.Reader, w cim.CIMLayerWriter) (int64, error) {
	tr := tar.NewReader(r)
	buf := bufio.NewWriter(w)
	size := int64(0)

	// Iterate through the files in the archive.
	hdr, loopErr := tr.Next()
	for loopErr == nil {
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		default:
		}

		// Note: path is used instead of filepath to prevent OS specific handling
		// of the tar path
		base := path.Base(hdr.Name)
		if strings.HasPrefix(base, ociwclayer.WhiteoutPrefix) {
			name := path.Join(path.Dir(hdr.Name), base[len(ociwclayer.WhiteoutPrefix):])
			if rErr := w.Remove(filepath.FromSlash(name)); rErr != nil {
				return 0, rErr
			}
			hdr, loopErr = tr.Next()
		} else if hdr.Typeflag == tar.TypeLink {
			if linkErr := w.AddLink(filepath.FromSlash(hdr.Name), filepath.FromSlash(hdr.Linkname)); linkErr != nil {
				return 0, linkErr
			}
			hdr, loopErr = tr.Next()
		} else {
			name, fileSize, fileInfo, err := backuptar.FileInfoFromHeader(hdr)
			if err != nil {
				return 0, err
			}
			sddl, err := backuptar.SecurityDescriptorFromTarHeader(hdr)
			if err != nil {
				return 0, err
			}
			eadata, err := backuptar.ExtendedAttributesFromTarHeader(hdr)
			if err != nil {
				return 0, err
			}

			var reparse []byte
			// As of now the only valid reparse data in a layer will be for a symlink. If file is
			// a symlink set reparse attribute and ensure reparse data buffer isn't
			// empty. Otherwise remove the reparse attributed.
			fileInfo.FileAttributes &^= uint32(windows.FILE_ATTRIBUTE_REPARSE_POINT)
			if hdr.Typeflag == tar.TypeSymlink {
				reparse = backuptar.EncodeReparsePointFromTarHeader(hdr)
				if len(reparse) > 0 {
					fileInfo.FileAttributes |= uint32(windows.FILE_ATTRIBUTE_REPARSE_POINT)
				}
			}

			if addErr := w.Add(filepath.FromSlash(name), fileInfo, fileSize, sddl, eadata, reparse); addErr != nil {
				return 0, addErr
			}
			if hdr.Typeflag == tar.TypeReg {
				if _, cpErr := io.Copy(buf, tr); cpErr != nil {
					return 0, cpErr
				}
			}
			size += fileSize

			// Copy all the alternate data streams and return the next non-ADS header.
			var ahdr *tar.Header
			for {
				ahdr, loopErr = tr.Next()
				if loopErr != nil {
					break
				}

				if ahdr.Typeflag != tar.TypeReg || !strings.HasPrefix(ahdr.Name, hdr.Name+":") {
					hdr = ahdr
					break
				}

				// stream names have following format: '<filename>:<stream name>:$DATA'
				// $DATA is one of the valid types of streams. We currently only support
				// data streams so fail if this is some other type of stream.
				if !strings.HasSuffix(ahdr.Name, ":$DATA") {
					return 0, fmt.Errorf("stream types other than $DATA are not supported, found: %s", ahdr.Name)
				}

				if addErr := w.AddAlternateStream(filepath.FromSlash(ahdr.Name), uint64(ahdr.Size)); addErr != nil {
					return 0, addErr
				}

				if _, cpErr := io.Copy(buf, tr); cpErr != nil {
					return 0, cpErr
				}
			}
		}

		if flushErr := buf.Flush(); flushErr != nil {
			if loopErr == nil {
				loopErr = flushErr
			} else {
				log.G(ctx).WithError(flushErr).Warn("flush buffer during layer write failed")
			}
		}
	}
	if !errors.Is(loopErr, io.EOF) {
		return 0, loopErr
	}
	return size, nil
}
