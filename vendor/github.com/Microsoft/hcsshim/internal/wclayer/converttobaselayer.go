package wclayer

import (
	"context"
	"os"
	"path/filepath"
	"syscall"

	"github.com/Microsoft/hcsshim/internal/hcserror"
	"github.com/Microsoft/hcsshim/internal/oc"
	"github.com/Microsoft/hcsshim/internal/safefile"
	"github.com/Microsoft/hcsshim/internal/winapi"
	"github.com/pkg/errors"
	"go.opencensus.io/trace"
)

var hiveNames = []string{"DEFAULT", "SAM", "SECURITY", "SOFTWARE", "SYSTEM"}

//go:generate go run mkminimalhive_windows.go -output zminimalhive_windows.go

// Ensure the given file exists as an ordinary file, and create a minimal hive file if not.
func ensureHive(path string, root *os.File) error {
	stat, err := safefile.LstatRelative(path, root)
	if err != nil && os.IsNotExist(err) {
		minimalHiveBytes, err := minimalHiveContents()
		if err != nil {
			return err
		}

		newFile, err := safefile.OpenRelative(path, root, syscall.GENERIC_WRITE, syscall.FILE_SHARE_WRITE, winapi.FILE_CREATE, 0)
		if err != nil {
			return err
		}

		_, err = newFile.Write(minimalHiveBytes)
		if err != nil {
			newFile.Close()
			return err
		}

		return newFile.Close()
	}

	if err != nil {
		return err
	}

	if !stat.Mode().IsRegular() {
		fullPath := filepath.Join(root.Name(), path)
		return errors.Errorf("%s has unexpected file mode %s", fullPath, stat.Mode().String())
	}

	return nil
}

func ensureBaseLayer(root *os.File) (hasUtilityVM bool, err error) {
	// The base layer registry hives will be copied from here
	const hiveSourcePath = "Files\\Windows\\System32\\config"
	if err = safefile.MkdirAllRelative(hiveSourcePath, root); err != nil {
		return
	}

	for _, hiveName := range hiveNames {
		hivePath := filepath.Join(hiveSourcePath, hiveName)
		if err = ensureHive(hivePath, root); err != nil {
			return
		}
	}

	stat, err := safefile.LstatRelative(utilityVMFilesPath, root)

	if os.IsNotExist(err) {
		return false, nil
	}

	if err != nil {
		return
	}

	if !stat.Mode().IsDir() {
		fullPath := filepath.Join(root.Name(), utilityVMFilesPath)
		return false, errors.Errorf("%s has unexpected file mode %s", fullPath, stat.Mode().String())
	}

	const bcdRelativePath = "EFI\\Microsoft\\Boot\\BCD"

	// Just check that this exists as a regular file. If it exists but is not a valid registry hive,
	// ProcessUtilityVMImage will complain:
	// "The registry could not read in, or write out, or flush, one of the files that contain the system's image of the registry."
	bcdPath := filepath.Join(utilityVMFilesPath, bcdRelativePath)

	stat, err = safefile.LstatRelative(bcdPath, root)
	if err != nil {
		return false, errors.Wrapf(err, "UtilityVM must contain '%s'", bcdRelativePath)
	}

	if !stat.Mode().IsRegular() {
		fullPath := filepath.Join(root.Name(), bcdPath)
		return false, errors.Errorf("%s has unexpected file mode %s", fullPath, stat.Mode().String())
	}

	return true, nil
}

func convertToBaseLayer(ctx context.Context, root *os.File) error {
	hasUtilityVM, err := ensureBaseLayer(root)

	if err != nil {
		return err
	}

	if err := ProcessBaseLayer(ctx, root.Name()); err != nil {
		return err
	}

	if !hasUtilityVM {
		return nil
	}

	err = safefile.EnsureNotReparsePointRelative(utilityVMPath, root)
	if err != nil {
		return err
	}

	utilityVMPath := filepath.Join(root.Name(), utilityVMPath)
	return ProcessUtilityVMImage(ctx, utilityVMPath)
}

// ConvertToBaseLayer processes a candidate base layer, i.e. a directory
// containing the desired file content under Files/, and optionally the
// desired file content for a UtilityVM under UtilityMV/Files/
func ConvertToBaseLayer(ctx context.Context, path string) (err error) {
	title := "hcsshim::ConvertToBaseLayer"
	ctx, span := trace.StartSpan(ctx, title)
	defer span.End()
	defer func() { oc.SetSpanStatus(span, err) }()
	span.AddAttributes(trace.StringAttribute("path", path))

	if root, err := safefile.OpenRoot(path); err != nil {
		return hcserror.New(err, title+" - failed", "")
	} else if err = convertToBaseLayer(ctx, root); err != nil {
		return hcserror.New(err, title+" - failed", "")
	}
	return nil
}
