package files

import (
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
)

// serialFile implements Node, and reads from a path on the OS filesystem.
// No more than one file will be opened at a time.
type serialFile struct {
	path   string
	files  []os.FileInfo
	stat   os.FileInfo
	filter *Filter
}

type serialIterator struct {
	files  []os.FileInfo
	path   string
	filter *Filter

	curName string
	curFile Node

	err error
}

// NewSerialFile takes a filepath, a bool specifying if hidden files should be included,
// and a fileInfo and returns a Node representing file, directory or special file.
func NewSerialFile(path string, includeHidden bool, stat os.FileInfo) (Node, error) {
	filter, err := NewFilter("", nil, includeHidden)
	if err != nil {
		return nil, err
	}
	return NewSerialFileWithFilter(path, filter, stat)
}

// NewSerialFileWith takes a filepath, a filter for determining which files should be
// operated upon if the filepath is a directory, and a fileInfo and returns a
// Node representing file, directory or special file.
func NewSerialFileWithFilter(path string, filter *Filter, stat os.FileInfo) (Node, error) {
	switch mode := stat.Mode(); {
	case mode.IsRegular():
		file, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		return NewReaderPathFile(path, file, stat)
	case mode.IsDir():
		// for directories, stat all of the contents first, so we know what files to
		// open when Entries() is called
		contents, err := ioutil.ReadDir(path)
		if err != nil {
			return nil, err
		}
		return &serialFile{path, contents, stat, filter}, nil
	case mode&os.ModeSymlink != 0:
		target, err := os.Readlink(path)
		if err != nil {
			return nil, err
		}
		return NewLinkFile(target, stat), nil
	default:
		return nil, fmt.Errorf("unrecognized file type for %s: %s", path, mode.String())
	}
}

func (it *serialIterator) Name() string {
	return it.curName
}

func (it *serialIterator) Node() Node {
	return it.curFile
}

func (it *serialIterator) Next() bool {
	// if there aren't any files left in the root directory, we're done
	if len(it.files) == 0 {
		return false
	}

	stat := it.files[0]
	it.files = it.files[1:]
	for it.filter.ShouldExclude(stat) {
		if len(it.files) == 0 {
			return false
		}

		stat = it.files[0]
		it.files = it.files[1:]
	}

	// open the next file
	filePath := filepath.ToSlash(filepath.Join(it.path, stat.Name()))

	// recursively call the constructor on the next file
	// if it's a regular file, we will open it as a ReaderFile
	// if it's a directory, files in it will be opened serially
	sf, err := NewSerialFileWithFilter(filePath, it.filter, stat)
	if err != nil {
		it.err = err
		return false
	}

	it.curName = stat.Name()
	it.curFile = sf
	return true
}

func (it *serialIterator) Err() error {
	return it.err
}

func (f *serialFile) Entries() DirIterator {
	return &serialIterator{
		path:   f.path,
		files:  f.files,
		filter: f.filter,
	}
}

func (f *serialFile) Close() error {
	return nil
}

func (f *serialFile) Stat() os.FileInfo {
	return f.stat
}

func (f *serialFile) Size() (int64, error) {
	if !f.stat.IsDir() {
		//something went terribly, terribly wrong
		return 0, errors.New("serialFile is not a directory")
	}

	var du int64
	err := filepath.Walk(f.path, func(p string, fi os.FileInfo, err error) error {
		if err != nil || fi == nil {
			return err
		}

		if f.filter.ShouldExclude(fi) {
			if fi.Mode().IsDir() {
				return filepath.SkipDir
			}
		} else if fi.Mode().IsRegular() {
			du += fi.Size()
		}

		return nil
	})

	return du, err
}

var _ Directory = &serialFile{}
var _ DirIterator = &serialIterator{}
