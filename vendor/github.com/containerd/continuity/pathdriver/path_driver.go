package pathdriver

import (
	"os"
	"path/filepath"
	"sort"
)

// PathDriver provides all of the path manipulation functions in a common
// interface. The context should call these and never use the `filepath`
// package or any other package to manipulate paths.
type PathDriver interface {
	Join(paths ...string) string
	IsAbs(path string) bool
	Rel(base, target string) (string, error)
	Base(path string) string
	Dir(path string) string
	Clean(path string) string
	Split(path string) (dir, file string)
	Separator() byte
	Abs(path string) (string, error)
	Walk(string, filepath.WalkFunc) error
	FromSlash(path string) string
	ToSlash(path string) string
	Match(pattern, name string) (matched bool, err error)
}

// pathDriver is a simple default implementation calls the filepath package.
type pathDriver struct{}

// LocalPathDriver is the exported pathDriver struct for convenience.
var LocalPathDriver PathDriver = &pathDriver{}

func (*pathDriver) Join(paths ...string) string {
	return filepath.Join(paths...)
}

func (*pathDriver) IsAbs(path string) bool {
	return filepath.IsAbs(path)
}

func (*pathDriver) Rel(base, target string) (string, error) {
	return filepath.Rel(base, target)
}

func (*pathDriver) Base(path string) string {
	return filepath.Base(path)
}

func (*pathDriver) Dir(path string) string {
	return filepath.Dir(path)
}

func (*pathDriver) Clean(path string) string {
	return filepath.Clean(path)
}

func (*pathDriver) Split(path string) (dir, file string) {
	return filepath.Split(path)
}

func (*pathDriver) Separator() byte {
	return filepath.Separator
}

func (*pathDriver) Abs(path string) (string, error) {
	return filepath.Abs(path)
}

// Note that filepath.Walk calls os.Stat, so if the context wants to
// to call Driver.Stat() for Walk, they need to create a new struct that
// overrides this method.
func (*pathDriver) Walk(root string, walkFn filepath.WalkFunc) error {
	return Walk(root, walkFn)
}

var lstat = os.Lstat // for testing

// walk recursively descends path, calling walkFn.
func walk(path string, info os.FileInfo, walkFn filepath.WalkFunc) error {
	if !info.IsDir() {
		return walkFn(path, info, nil)
	}

	names, err := readDirNames(path)
	err1 := walkFn(path, info, err)
	// If err != nil, walk can't walk into this directory.
	// err1 != nil means walkFn want walk to skip this directory or stop walking.
	// Therefore, if one of err and err1 isn't nil, walk will return.
	if err != nil || err1 != nil {
		// The caller's behavior is controlled by the return value, which is decided
		// by walkFn. walkFn may ignore err and return nil.
		// If walkFn returns SkipDir, it will be handled by the caller.
		// So walk should return whatever walkFn returns.
		return err1
	}

	for _, name := range names {
		filename := filepath.Join(path, name)
		fileInfo, err := lstat(filename)
		if err != nil {
			if err := walkFn(filename, fileInfo, err); err != nil && err != filepath.SkipDir {
				return err
			}
		} else {
			err = walk(filename, fileInfo, walkFn)
			if err != nil {
				if !fileInfo.IsDir() || err != filepath.SkipDir {
					return err
				}
			}
		}
	}
	return nil
}

// Walk walks the file tree rooted at root, calling walkFn for each file or
// directory in the tree, including root. All errors that arise visiting files
// and directories are filtered by walkFn. The files are walked in lexical
// order, which makes the output deterministic but means that for very
// large directories Walk can be inefficient.
// Walk does not follow symbolic links.
func Walk(root string, walkFn filepath.WalkFunc) error {
	info, err := os.Stat(root)
	if err != nil {
		err = walkFn(root, nil, err)
	} else {
		err = walk(root, info, walkFn)
	}
	if err == filepath.SkipDir {
		return nil
	}
	return err
}

// readDirNames reads the directory named by dirname and returns
// a sorted list of directory entries.
func readDirNames(dirname string) ([]string, error) {
	f, err := os.Open(dirname)
	if err != nil {
		return nil, err
	}
	names, err := f.Readdirnames(-1)
	f.Close()
	if err != nil {
		return nil, err
	}
	sort.Strings(names)
	return names, nil
}

func (*pathDriver) FromSlash(path string) string {
	return filepath.FromSlash(path)
}

func (*pathDriver) ToSlash(path string) string {
	return filepath.ToSlash(path)
}

func (*pathDriver) Match(pattern, name string) (bool, error) {
	return filepath.Match(pattern, name)
}
