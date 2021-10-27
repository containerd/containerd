package files

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// WriteTo writes the given node to the local filesystem at fpath.
func WriteTo(nd Node, fpath string) error {
	switch nd := nd.(type) {
	case *Symlink:
		return os.Symlink(nd.Target, fpath)
	case File:
		f, err := os.Create(fpath)
		defer f.Close()
		if err != nil {
			return err
		}
		_, err = io.Copy(f, nd)
		if err != nil {
			return err
		}
		return nil
	case Directory:
		err := os.Mkdir(fpath, 0777)
		if err != nil {
			return err
		}

		entries := nd.Entries()
		for entries.Next() {
			child := filepath.Join(fpath, entries.Name())
			if err := WriteTo(entries.Node(), child); err != nil {
				return err
			}
		}
		return entries.Err()
	default:
		return fmt.Errorf("file type %T at %q is not supported", nd, fpath)
	}
}
