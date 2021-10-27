package http

import "io"

// bodyWrapper wraps an io.Reader and calls onEOF whenever the Read function returns io.EOF.
// This was designed for wrapping the request body, so we can know whether it was closed.
type bodyWrapper struct {
	io.ReadCloser
	onEOF func()
}

func (bw bodyWrapper) Read(data []byte) (int, error) {
	n, err := bw.ReadCloser.Read(data)
	if err == io.EOF {
		bw.onEOF()
	}

	return n, err
}
