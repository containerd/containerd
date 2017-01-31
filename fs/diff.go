package fs

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/Sirupsen/logrus"
)

// ChangeKind is the type of modification that
// a change is making.
type ChangeKind int

const (
	// ChangeKindAdd represents an addition of
	// a file
	ChangeKindAdd = iota

	// ChangeKindModify represents a change to
	// an existing file
	ChangeKindModify

	// ChangeKindDelete represents a delete of
	// a file
	ChangeKindDelete
)

func (k ChangeKind) String() string {
	switch k {
	case ChangeKindAdd:
		return "add"
	case ChangeKindModify:
		return "modify"
	case ChangeKindDelete:
		return "delete"
	default:
		return ""
	}
}

// Change represents single change between a diff and its parent.
type Change struct {
	Kind     ChangeKind
	Path     string
	FileInfo os.FileInfo
	Source   string
}

// Changes returns a stream of changes between the provided upper
// directory and lower directory.
//
// Changes are ordered by name and should be appliable in the
// order in which they received.
//  Due to this apply ordering, the following is true
//  - Removed directory trees only create a single change for the root
//    directory removed. Remaining changes are implied.
//  - A directory which is modified to become a file will not have
//    delete entries for sub-path items, their removal is implied
//    by the removal of the parent directory.
//
// Opaque directories will not be treated specially and each file
// removed from the lower will show up as a removal
//
// File content comparisons will be done on files which have timestamps
// which may have been truncated. If either of the files being compared
// has a zero value nanosecond value, each byte will be compared for
// differences. If 2 files have the same seconds value but different
// nanosecond values where one of those values is zero, the files will
// be considered unchanged if the content is the same. This behavior
// is to account for timestamp truncation during archiving.
func Changes(ctx context.Context, upper, lower string) (context.Context, <-chan Change) {
	var (
		changes        = make(chan Change)
		retCtx, cancel = context.WithCancel(ctx)
	)

	cc := &changeContext{
		Context: retCtx,
	}

	go func() {
		var err error
		if lower == "" {
			logrus.Debugf("Using single walk diff for %s", upper)
			err = addDirChanges(ctx, changes, upper)
		} else if diffOptions := detectDirDiff(upper, lower); diffOptions != nil {
			logrus.Debugf("Using single walk diff for %s from %s", diffOptions.diffDir, lower)
			err = diffDirChanges(ctx, changes, lower, diffOptions)
		} else {
			logrus.Debugf("Using double walk diff for %s from %s", upper, lower)
			err = doubleWalkDiff(ctx, changes, upper, lower)
		}

		if err != nil {
			cc.errL.Lock()
			cc.err = err
			cc.errL.Unlock()
			cancel()
		}
		defer close(changes)
	}()

	return cc, changes
}

// changeContext wraps a context to allow setting an error
// directly from a change streamer to allow streams canceled
// due to errors to propagate the error to the caller.
type changeContext struct {
	context.Context

	err  error
	errL sync.Mutex
}

func (cc *changeContext) Err() error {
	cc.errL.Lock()
	if cc.err != nil {
		return cc.err
	}
	cc.errL.Unlock()
	return cc.Context.Err()
}

func sendChange(ctx context.Context, changes chan<- Change, change Change) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case changes <- change:
		return nil
	}
}

func addDirChanges(ctx context.Context, changes chan<- Change, root string) error {
	return filepath.Walk(root, func(path string, f os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Rebase path
		path, err = filepath.Rel(root, path)
		if err != nil {
			return err
		}

		path = filepath.Join(string(os.PathSeparator), path)

		// Skip root
		if path == string(os.PathSeparator) {
			return nil
		}

		change := Change{
			Path:     path,
			Kind:     ChangeKindAdd,
			FileInfo: f,
			Source:   filepath.Join(root, path),
		}

		return sendChange(ctx, changes, change)
	})
}

// diffDirOptions is used when the diff can be directly calculated from
// a diff directory to its lower, without walking both trees.
type diffDirOptions struct {
	diffDir      string
	skipChange   func(string) (bool, error)
	deleteChange func(string, string, os.FileInfo) (string, error)
}

// diffDirChanges walks the diff directory and compares changes against the lower.
func diffDirChanges(ctx context.Context, changes chan<- Change, lower string, o *diffDirOptions) error {
	changedDirs := make(map[string]struct{})
	return filepath.Walk(o.diffDir, func(path string, f os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Rebase path
		path, err = filepath.Rel(o.diffDir, path)
		if err != nil {
			return err
		}

		path = filepath.Join(string(os.PathSeparator), path)

		// Skip root
		if path == string(os.PathSeparator) {
			return nil
		}

		// TODO: handle opaqueness, start new double walker at this
		// location to get deletes, and skip tree in single walker

		if o.skipChange != nil {
			if skip, err := o.skipChange(path); skip {
				return err
			}
		}

		change := Change{
			Path: path,
		}

		deletedFile, err := o.deleteChange(o.diffDir, path, f)
		if err != nil {
			return err
		}

		// Find out what kind of modification happened
		if deletedFile != "" {
			change.Path = deletedFile
			change.Kind = ChangeKindDelete
		} else {
			// Otherwise, the file was added
			change.Kind = ChangeKindAdd
			change.FileInfo = f
			change.Source = filepath.Join(o.diffDir, path)

			// ...Unless it already existed in a lower, in which case, it's a modification
			stat, err := os.Stat(filepath.Join(lower, path))
			if err != nil && !os.IsNotExist(err) {
				return err
			}
			if err == nil {
				// The file existed in the lower, so that's a modification

				// However, if it's a directory, maybe it wasn't actually modified.
				// If you modify /foo/bar/baz, then /foo will be part of the changed files only because it's the parent of bar
				if stat.IsDir() && f.IsDir() {
					if f.Size() == stat.Size() && f.Mode() == stat.Mode() && sameFsTime(f.ModTime(), stat.ModTime()) {
						// Both directories are the same, don't record the change
						return nil
					}
				}
				change.Kind = ChangeKindModify
			}
		}

		// If /foo/bar/file.txt is modified, then /foo/bar must be part of the changed files.
		// This block is here to ensure the change is recorded even if the
		// modify time, mode and size of the parent directory in the rw and ro layers are all equal.
		// Check https://github.com/docker/docker/pull/13590 for details.
		if f.IsDir() {
			changedDirs[path] = struct{}{}
		}
		if change.Kind == ChangeKindAdd || change.Kind == ChangeKindDelete {
			parent := filepath.Dir(path)
			if _, ok := changedDirs[parent]; !ok && parent != "/" {
				pi, err := os.Stat(filepath.Join(o.diffDir, parent))
				if err != nil {
					return err
				}
				dirChange := Change{
					Path:     parent,
					Kind:     ChangeKindModify,
					FileInfo: pi,
					Source:   filepath.Join(o.diffDir, parent),
				}
				if err := sendChange(ctx, changes, dirChange); err != nil {
					return err
				}
				changedDirs[parent] = struct{}{}
			}
		}

		return sendChange(ctx, changes, change)
	})
}

// doubleWalkDiff walks both directories to create a diff
func doubleWalkDiff(ctx context.Context, changes chan<- Change, upper, lower string) (err error) {
	pathCtx, cancel := context.WithCancel(ctx)
	defer func() {
		if err != nil {
			cancel()
		}
	}()

	var (
		w1     = pathWalker(pathCtx, lower)
		w2     = pathWalker(pathCtx, upper)
		f1, f2 *currentPath
		rmdir  string
	)

	for w1 != nil || w2 != nil {
		if f1 == nil && w1 != nil {
			f1, err = nextPath(w1)
			if err != nil {
				return err
			}
			if f1 == nil {
				w1 = nil
			}
		}

		if f2 == nil && w2 != nil {
			f2, err = nextPath(w2)
			if err != nil {
				return err
			}
			if f2 == nil {
				w2 = nil
			}
		}
		if f1 == nil && f2 == nil {
			continue
		}

		c := pathChange(f1, f2)
		switch c.Kind {
		case ChangeKindAdd:
			if rmdir != "" {
				rmdir = ""
			}
			c.FileInfo = f2.f
			c.Source = filepath.Join(upper, c.Path)
			f2 = nil
		case ChangeKindDelete:
			// Check if this file is already removed by being
			// under of a removed directory
			if rmdir != "" && strings.HasPrefix(f1.path, rmdir) {
				f1 = nil
				continue
			} else if rmdir == "" && f1.f.IsDir() {
				rmdir = f1.path + string(os.PathSeparator)
			} else if rmdir != "" {
				rmdir = ""
			}
			f1 = nil
		case ChangeKindModify:
			same, err := sameFile(f1, f2)
			if err != nil {
				return err
			}
			if f1.f.IsDir() && !f2.f.IsDir() {
				rmdir = f1.path + string(os.PathSeparator)
			} else if rmdir != "" {
				rmdir = ""
			}
			c.FileInfo = f2.f
			c.Source = filepath.Join(upper, c.Path)
			f1 = nil
			f2 = nil
			if same {
				continue
			}
		}
		if err := sendChange(ctx, changes, c); err != nil {
			return err
		}
	}

	return nil
}
