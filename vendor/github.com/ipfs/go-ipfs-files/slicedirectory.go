package files

import "sort"

type fileEntry struct {
	name string
	file Node
}

func (e fileEntry) Name() string {
	return e.name
}

func (e fileEntry) Node() Node {
	return e.file
}

func FileEntry(name string, file Node) DirEntry {
	return fileEntry{
		name: name,
		file: file,
	}
}

type sliceIterator struct {
	files []DirEntry
	n     int
}

func (it *sliceIterator) Name() string {
	return it.files[it.n].Name()
}

func (it *sliceIterator) Node() Node {
	return it.files[it.n].Node()
}

func (it *sliceIterator) Next() bool {
	it.n++
	return it.n < len(it.files)
}

func (it *sliceIterator) Err() error {
	return nil
}

// SliceFile implements Node, and provides simple directory handling.
// It contains children files, and is created from a `[]Node`.
// SliceFiles are always directories, and can't be read from or closed.
type SliceFile struct {
	files []DirEntry
}

func NewMapDirectory(f map[string]Node) Directory {
	ents := make([]DirEntry, 0, len(f))
	for name, nd := range f {
		ents = append(ents, FileEntry(name, nd))
	}
	sort.Slice(ents, func(i, j int) bool {
		return ents[i].Name() < ents[j].Name()
	})

	return NewSliceDirectory(ents)
}

func NewSliceDirectory(files []DirEntry) Directory {
	return &SliceFile{files}
}

func (f *SliceFile) Entries() DirIterator {
	return &sliceIterator{files: f.files, n: -1}
}

func (f *SliceFile) Close() error {
	return nil
}

func (f *SliceFile) Length() int {
	return len(f.files)
}

func (f *SliceFile) Size() (int64, error) {
	var size int64

	for _, file := range f.files {
		s, err := file.Node().Size()
		if err != nil {
			return 0, err
		}
		size += s
	}

	return size, nil
}

var _ Directory = &SliceFile{}
var _ DirEntry = fileEntry{}
