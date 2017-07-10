package console

import (
	"fmt"
	"os"

	"github.com/Azure/go-ansiterm/winterm"
	"github.com/pkg/errors"
)

const (
	// https://msdn.microsoft.com/en-us/library/windows/desktop/ms683167(v=vs.85).aspx
	enableVirtualTerminalInput      = 0x0200
	enableVirtualTerminalProcessing = 0x0004
	disableNewlineAutoReturn        = 0x0008
)

var (
	vtInputSupported  bool
	ErrNotImplemented = errors.New("not implemented")
)

func (m *master) initStdios() {
	m.in = os.Stdin.Fd()
	if mode, err := winterm.GetConsoleMode(m.in); err == nil {
		m.inMode = mode
		// Validate that enableVirtualTerminalInput is supported, but do not set it.
		if err = winterm.SetConsoleMode(m.in, mode|enableVirtualTerminalInput); err == nil {
			vtInputSupported = true
		}
		// Unconditionally set the console mode back even on failure because SetConsoleMode
		// remembers invalid bits on input handles.
		winterm.SetConsoleMode(m.in, mode)
	} else {
		fmt.Printf("failed to get console mode for stdin: %v\n", err)
	}

	m.out = os.Stdout.Fd()
	if mode, err := winterm.GetConsoleMode(m.out); err == nil {
		m.outMode = mode
		if err := winterm.SetConsoleMode(m.out, mode|enableVirtualTerminalProcessing); err == nil {
			m.outMode |= enableVirtualTerminalProcessing
		} else {
			winterm.SetConsoleMode(m.out, m.outMode)
		}
	} else {
		fmt.Printf("failed to get console mode for stdout: %v\n", err)
	}

	m.err = os.Stderr.Fd()
	if mode, err := winterm.GetConsoleMode(m.err); err == nil {
		m.errMode = mode
		if err := winterm.SetConsoleMode(m.err, mode|enableVirtualTerminalProcessing); err == nil {
			m.errMode |= enableVirtualTerminalProcessing
		} else {
			winterm.SetConsoleMode(m.err, m.errMode)
		}
	} else {
		fmt.Printf("failed to get console mode for stderr: %v\n", err)
	}
}

type master struct {
	in     uintptr
	inMode uint32

	out     uintptr
	outMode uint32

	err     uintptr
	errMode uint32
}

func (m *master) SetRaw() error {
	if err := makeInputRaw(m.in, m.inMode); err != nil {
		return err
	}

	// Set StdOut and StdErr to raw mode, we ignore failures since
	// disableNewlineAutoReturn might not be supported on this version of
	// Windows.

	winterm.SetConsoleMode(m.out, m.outMode|disableNewlineAutoReturn)

	winterm.SetConsoleMode(m.err, m.errMode|disableNewlineAutoReturn)

	return nil
}

func (m *master) Reset() error {
	for _, s := range []struct {
		fd   uintptr
		mode uint32
	}{
		{m.in, m.inMode},
		{m.out, m.outMode},
		{m.err, m.errMode},
	} {
		if err := winterm.SetConsoleMode(s.fd, s.mode); err != nil {
			return errors.Wrap(err, "unable to restore console mode")
		}
	}

	return nil
}

func (m *master) Size() (WinSize, error) {
	info, err := winterm.GetConsoleScreenBufferInfo(m.out)
	if err != nil {
		return WinSize{}, errors.Wrap(err, "unable to get console info")
	}

	winsize := WinSize{
		Width:  uint16(info.Window.Right - info.Window.Left + 1),
		Height: uint16(info.Window.Bottom - info.Window.Top + 1),
	}

	return winsize, nil
}

func (m *master) Resize(ws WinSize) error {
	return ErrNotImplemented
}

func (m *master) ResizeFrom(c Console) error {
	return ErrNotImplemented
}

func (m *master) DisableEcho() error {
	mode := m.inMode &^ winterm.ENABLE_ECHO_INPUT
	mode |= winterm.ENABLE_PROCESSED_INPUT
	mode |= winterm.ENABLE_LINE_INPUT

	if err := winterm.SetConsoleMode(m.in, mode); err != nil {
		return errors.Wrap(err, "unable to set console to disable echo")
	}

	return nil
}

func (m *master) Close() error {
	return nil
}

func (m *master) Read(b []byte) (int, error) {
	panic("not implemented on windows")
}

func (m *master) Write(b []byte) (int, error) {
	panic("not implemented on windows")
}

func (m *master) Fd() uintptr {
	return m.in
}

// makeInputRaw puts the terminal (Windows Console) connected to the given
// file descriptor into raw mode
func makeInputRaw(fd uintptr, mode uint32) error {
	// See
	// -- https://msdn.microsoft.com/en-us/library/windows/desktop/ms686033(v=vs.85).aspx
	// -- https://msdn.microsoft.com/en-us/library/windows/desktop/ms683462(v=vs.85).aspx

	// Disable these modes
	mode &^= winterm.ENABLE_ECHO_INPUT
	mode &^= winterm.ENABLE_LINE_INPUT
	mode &^= winterm.ENABLE_MOUSE_INPUT
	mode &^= winterm.ENABLE_WINDOW_INPUT
	mode &^= winterm.ENABLE_PROCESSED_INPUT

	// Enable these modes
	mode |= winterm.ENABLE_EXTENDED_FLAGS
	mode |= winterm.ENABLE_INSERT_MODE
	mode |= winterm.ENABLE_QUICK_EDIT_MODE

	if vtInputSupported {
		mode |= enableVirtualTerminalInput
	}

	if err := winterm.SetConsoleMode(fd, mode); err != nil {
		return errors.Wrap(err, "unable to set console to raw mode")
	}

	return nil
}

func checkConsole(f *os.File) error {
	if _, err := winterm.GetConsoleMode(f.Fd()); err != nil {
		return err
	}
	return nil
}

func newMaster(f *os.File) Console {
	if f != os.Stdin && f != os.Stdout && f != os.Stderr {
		panic("creating a console from a file is not supported on windows")
	}

	m := &master{}
	m.initStdios()

	return m
}
