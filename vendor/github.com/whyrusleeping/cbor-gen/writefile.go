package typegen

import (
	"bytes"
	"go/format"
	"os"
	"os/exec"

	"golang.org/x/xerrors"
)

func WriteTupleEncodersToFile(fname, pkg string, types ...interface{}) error {
	buf := new(bytes.Buffer)

	if err := PrintHeaderAndUtilityMethods(buf, pkg); err != nil {
		return xerrors.Errorf("failed to write header: %w", err)
	}

	for _, t := range types {
		if err := GenTupleEncodersForType(pkg, t, buf); err != nil {
			return xerrors.Errorf("failed to generate encoders: %w", err)
		}
	}

	data, err := format.Source(buf.Bytes())
	if err != nil {
		return err
	}

	fi, err := os.Create(fname)
	if err != nil {
		return xerrors.Errorf("failed to open file: %w", err)
	}

	_, err = fi.Write(data)
	if err != nil {
		_ = fi.Close()
		return err
	}
	_ = fi.Close()

	if err := exec.Command("goimports", "-w", fname).Run(); err != nil {
		return err
	}

	return nil
}

func WriteMapEncodersToFile(fname, pkg string, types ...interface{}) error {
	buf := new(bytes.Buffer)

	if err := PrintHeaderAndUtilityMethods(buf, pkg); err != nil {
		return xerrors.Errorf("failed to write header: %w", err)
	}

	for _, t := range types {
		if err := GenMapEncodersForType(pkg, t, buf); err != nil {
			return xerrors.Errorf("failed to generate encoders: %w", err)
		}
	}

	data, err := format.Source(buf.Bytes())
	if err != nil {
		return err
	}

	fi, err := os.Create(fname)
	if err != nil {
		return xerrors.Errorf("failed to open file: %w", err)
	}

	_, err = fi.Write(data)
	if err != nil {
		_ = fi.Close()
		return err
	}
	_ = fi.Close()

	if err := exec.Command("goimports", "-w", fname).Run(); err != nil {
		return err
	}

	return nil
}
