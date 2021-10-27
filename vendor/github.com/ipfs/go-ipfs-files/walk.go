package files

import (
	"path/filepath"
)

// Walk walks a file tree, like `os.Walk`.
func Walk(nd Node, cb func(fpath string, nd Node) error) error {
	var helper func(string, Node) error
	helper = func(path string, nd Node) error {
		if err := cb(path, nd); err != nil {
			return err
		}
		dir, ok := nd.(Directory)
		if !ok {
			return nil
		}
		iter := dir.Entries()
		for iter.Next() {
			if err := helper(filepath.Join(path, iter.Name()), iter.Node()); err != nil {
				return err
			}
		}
		return iter.Err()
	}
	return helper("", nd)
}
