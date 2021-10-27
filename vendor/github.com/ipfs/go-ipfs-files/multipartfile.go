package files

import (
	"io"
	"io/ioutil"
	"mime"
	"mime/multipart"
	"net/url"
	"path"
	"strings"
)

const (
	multipartFormdataType = "multipart/form-data"
	multipartMixedType    = "multipart/mixed"

	applicationDirectory = "application/x-directory"
	applicationSymlink   = "application/symlink"
	applicationFile      = "application/octet-stream"

	contentTypeHeader = "Content-Type"
)

type multipartDirectory struct {
	path   string
	walker *multipartWalker

	// part is the part describing the directory. It's nil when implicit.
	part *multipart.Part
}

type multipartWalker struct {
	part   *multipart.Part
	reader *multipart.Reader
}

func (m *multipartWalker) consumePart() {
	m.part = nil
}

func (m *multipartWalker) getPart() (*multipart.Part, error) {
	if m.part != nil {
		return m.part, nil
	}
	if m.reader == nil {
		return nil, io.EOF
	}

	var err error
	m.part, err = m.reader.NextPart()
	if err == io.EOF {
		m.reader = nil
	}
	return m.part, err
}

// NewFileFromPartReader creates a Directory from a multipart reader.
func NewFileFromPartReader(reader *multipart.Reader, mediatype string) (Directory, error) {
	switch mediatype {
	case applicationDirectory, multipartFormdataType:
	default:
		return nil, ErrNotDirectory
	}

	return &multipartDirectory{
		path: "/",
		walker: &multipartWalker{
			reader: reader,
		},
	}, nil
}

func (w *multipartWalker) nextFile() (Node, error) {
	part, err := w.getPart()
	if err != nil {
		return nil, err
	}
	w.consumePart()

	contentType := part.Header.Get(contentTypeHeader)
	if contentType != "" {
		var err error
		contentType, _, err = mime.ParseMediaType(contentType)
		if err != nil {
			return nil, err
		}
	}

	switch contentType {
	case multipartFormdataType, applicationDirectory:
		return &multipartDirectory{
			part:   part,
			path:   fileName(part),
			walker: w,
		}, nil
	case applicationSymlink:
		out, err := ioutil.ReadAll(part)
		if err != nil {
			return nil, err
		}

		return NewLinkFile(string(out), nil), nil
	default:
		return &ReaderFile{
			reader:  part,
			abspath: part.Header.Get("abspath"),
		}, nil
	}
}

// fileName returns a normalized filename from a part.
func fileName(part *multipart.Part) string {
	v := part.Header.Get("Content-Disposition")
	_, params, err := mime.ParseMediaType(v)
	if err != nil {
		return ""
	}
	filename := params["filename"]
	if escaped, err := url.QueryUnescape(filename); err == nil {
		filename = escaped
	} // if there is a unescape error, just treat the name as unescaped

	return path.Clean("/" + filename)
}

// dirName appends a slash to the end of the filename, if not present.
// expects a _cleaned_ path.
func dirName(filename string) string {
	if !strings.HasSuffix(filename, "/") {
		filename += "/"
	}
	return filename
}

// isChild checks if child is a child of parent directory.
// expects a _cleaned_ path.
func isChild(child, parent string) bool {
	return strings.HasPrefix(child, dirName(parent))
}

// makeRelative makes the child path relative to the parent path.
// expects a _cleaned_ path.
func makeRelative(child, parent string) string {
	return strings.TrimPrefix(child, dirName(parent))
}

type multipartIterator struct {
	f *multipartDirectory

	curFile Node
	curName string
	err     error
}

func (it *multipartIterator) Name() string {
	return it.curName
}

func (it *multipartIterator) Node() Node {
	return it.curFile
}

func (it *multipartIterator) Next() bool {
	if it.f.walker.reader == nil || it.err != nil {
		return false
	}
	var part *multipart.Part
	for {
		part, it.err = it.f.walker.getPart()
		if it.err != nil {
			return false
		}

		name := fileName(part)

		// Is the file in a different directory?
		if !isChild(name, it.f.path) {
			return false
		}

		// Have we already entered this directory?
		if it.curName != "" && isChild(name, path.Join(it.f.path, it.curName)) {
			it.f.walker.consumePart()
			continue
		}

		// Make the path relative to the current directory.
		name = makeRelative(name, it.f.path)

		// Check if we need to create a fake directory (more than one
		// path component).
		if idx := strings.IndexByte(name, '/'); idx >= 0 {
			it.curName = name[:idx]
			it.curFile = &multipartDirectory{
				path:   path.Join(it.f.path, it.curName),
				walker: it.f.walker,
			}
			return true
		}
		it.curName = name

		// Finally, advance to the next file.
		it.curFile, it.err = it.f.walker.nextFile()

		return it.err == nil
	}
}

func (it *multipartIterator) Err() error {
	// We use EOF to signal that this iterator is done. That way, we don't
	// need to check every time `Next` is called.
	if it.err == io.EOF {
		return nil
	}
	return it.err
}

func (f *multipartDirectory) Entries() DirIterator {
	return &multipartIterator{f: f}
}

func (f *multipartDirectory) Close() error {
	if f.part != nil {
		return f.part.Close()
	}
	return nil
}

func (f *multipartDirectory) Size() (int64, error) {
	return 0, ErrNotSupported
}

var _ Directory = &multipartDirectory{}
