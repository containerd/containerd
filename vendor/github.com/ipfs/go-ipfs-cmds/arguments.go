package cmds

import (
	"bufio"
	"io"
)

// StdinArguments is used to iterate through arguments piped through stdin.
//
// It closely mimics the bufio.Scanner interface but also implements the
// ReadCloser interface.
type StdinArguments interface {
	io.ReadCloser

	// Scan reads in the next argument and returns true if there is an
	// argument to read.
	Scan() bool

	// Argument returns the next argument.
	Argument() string

	// Err returns any errors encountered when reading in arguments.
	Err() error
}

type arguments struct {
	argument string
	err      error
	reader   *bufio.Reader
	closer   io.Closer
}

func newArguments(r io.ReadCloser) *arguments {
	return &arguments{
		reader: bufio.NewReader(r),
		closer: r,
	}
}

// Read implements the io.Reader interface.
func (a *arguments) Read(b []byte) (int, error) {
	return a.reader.Read(b)
}

// Close implements the io.Closer interface.
func (a *arguments) Close() error {
	return a.closer.Close()
}

// WriteTo implements the io.WriterTo interface.
func (a *arguments) WriteTo(w io.Writer) (int64, error) {
	return a.reader.WriteTo(w)
}

// Err returns any errors encountered when reading in arguments.
func (a *arguments) Err() error {
	if a.err == io.EOF {
		return nil
	}
	return a.err
}

// Argument returns the last argument read in.
func (a *arguments) Argument() string {
	return a.argument
}

// Scan reads in the next argument and returns true if there is an
// argument to read.
func (a *arguments) Scan() bool {
	if a.err != nil {
		return false
	}

	s, err := a.reader.ReadString('\n')
	if err != nil {
		a.err = err
		if err == io.EOF && len(s) > 0 {
			a.argument = s
			return true
		}
		return false
	}

	l := len(s)
	if l >= 2 && s[l-2] == '\r' {
		a.argument = s[:l-2]
	} else {
		a.argument = s[:l-1]
	}
	return true
}
