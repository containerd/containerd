package files

import (
	"bytes"
	"fmt"
	"io"
	"mime/multipart"
	"net/textproto"
	"net/url"
	"path"
	"sync"
)

// MultiFileReader reads from a `commands.Node` (which can be a directory of files
// or a regular file) as HTTP multipart encoded data.
type MultiFileReader struct {
	io.Reader

	// directory stack for NextFile
	files []DirIterator
	path  []string

	currentFile Node
	buf         bytes.Buffer
	mpWriter    *multipart.Writer
	closed      bool
	mutex       *sync.Mutex

	// if true, the content disposition will be "form-data"
	// if false, the content disposition will be "attachment"
	form bool
}

// NewMultiFileReader constructs a MultiFileReader. `file` can be any `commands.Directory`.
// If `form` is set to true, the Content-Disposition will be "form-data".
// Otherwise, it will be "attachment".
func NewMultiFileReader(file Directory, form bool) *MultiFileReader {
	it := file.Entries()

	mfr := &MultiFileReader{
		files: []DirIterator{it},
		path:  []string{""},
		form:  form,
		mutex: &sync.Mutex{},
	}
	mfr.mpWriter = multipart.NewWriter(&mfr.buf)

	return mfr
}

func (mfr *MultiFileReader) Read(buf []byte) (written int, err error) {
	mfr.mutex.Lock()
	defer mfr.mutex.Unlock()

	// if we are closed and the buffer is flushed, end reading
	if mfr.closed && mfr.buf.Len() == 0 {
		return 0, io.EOF
	}

	// if the current file isn't set, advance to the next file
	if mfr.currentFile == nil {
		var entry DirEntry

		for entry == nil {
			if len(mfr.files) == 0 {
				mfr.mpWriter.Close()
				mfr.closed = true
				return mfr.buf.Read(buf)
			}

			if !mfr.files[len(mfr.files)-1].Next() {
				if mfr.files[len(mfr.files)-1].Err() != nil {
					return 0, mfr.files[len(mfr.files)-1].Err()
				}
				mfr.files = mfr.files[:len(mfr.files)-1]
				mfr.path = mfr.path[:len(mfr.path)-1]
				continue
			}

			entry = mfr.files[len(mfr.files)-1]
		}

		// handle starting a new file part
		if !mfr.closed {

			mfr.currentFile = entry.Node()

			// write the boundary and headers
			header := make(textproto.MIMEHeader)
			filename := url.QueryEscape(path.Join(path.Join(mfr.path...), entry.Name()))
			dispositionPrefix := "attachment"
			if mfr.form {
				dispositionPrefix = "form-data; name=\"file\""
			}

			header.Set("Content-Disposition", fmt.Sprintf("%s; filename=\"%s\"", dispositionPrefix, filename))

			var contentType string

			switch f := entry.Node().(type) {
			case *Symlink:
				contentType = "application/symlink"
			case Directory:
				newIt := f.Entries()
				mfr.files = append(mfr.files, newIt)
				mfr.path = append(mfr.path, entry.Name())
				contentType = "application/x-directory"
			case File:
				// otherwise, use the file as a reader to read its contents
				contentType = "application/octet-stream"
			default:
				return 0, ErrNotSupported
			}

			header.Set("Content-Type", contentType)
			if rf, ok := entry.Node().(FileInfo); ok {
				header.Set("abspath", rf.AbsPath())
			}

			_, err := mfr.mpWriter.CreatePart(header)
			if err != nil {
				return 0, err
			}
		}
	}

	// if the buffer has something in it, read from it
	if mfr.buf.Len() > 0 {
		return mfr.buf.Read(buf)
	}

	// otherwise, read from file data
	switch f := mfr.currentFile.(type) {
	case File:
		written, err = f.Read(buf)
		if err != io.EOF {
			return written, err
		}
	}

	if err := mfr.currentFile.Close(); err != nil {
		return written, err
	}

	mfr.currentFile = nil
	return written, nil
}

// Boundary returns the boundary string to be used to separate files in the multipart data
func (mfr *MultiFileReader) Boundary() string {
	return mfr.mpWriter.Boundary()
}
