package content

import (
	"io"
	"os"
)

// readerat implements io.ReaderAt in a completely stateless manner by opening
// the referenced file for each call to ReadAt.
type readerAt struct {
	f string
}

func (ra readerAt) ReadAt(p []byte, offset int64) (int, error) {
	fp, err := os.Open(ra.f)
	if err != nil {
		return 0, err
	}
	defer fp.Close()

	if _, err := fp.Seek(offset, io.SeekStart); err != nil {
		return 0, err
	}

	return fp.Read(p)
}
