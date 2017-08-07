// +build linux solaris

package console

import (
	"os"

	"golang.org/x/sys/unix"
)

func tcget(fd uintptr, p *unix.Termios) error {
	termios, err := unix.IoctlGetTermios(int(fd), unix.TCGETS)
	if err != nil {
		return err
	}
	*p = *termios
	return nil
}

func tcset(fd uintptr, p *unix.Termios) error {
	return unix.IoctlSetTermios(int(fd), unix.TCSETS, p)
}

func tcgwinsz(fd uintptr) (WinSize, error) {
	var ws WinSize

	uws, err := unix.IoctlGetWinsize(int(fd), unix.TIOCGWINSZ)
	if err != nil {
		return ws, err
	}

	// Translate from unix.Winsize to console.WinSize
	ws.Height = uws.Row
	ws.Width = uws.Col
	ws.x = uws.Xpixel
	ws.y = uws.Ypixel
	return ws, nil
}

func tcswinsz(fd uintptr, ws WinSize) error {
	// Translate from console.WinSize to unix.Winsize

	var uws unix.Winsize
	uws.Row = ws.Height
	uws.Col = ws.Width
	uws.Xpixel = ws.x
	uws.Ypixel = ws.y

	return unix.IoctlSetWinsize(int(fd), unix.TIOCSWINSZ, &uws)
}

func saneTerminal(f *os.File) error {
	var termios unix.Termios
	if err := tcget(f.Fd(), &termios); err != nil {
		return err
	}
	// Set -onlcr so we don't have to deal with \r.
	termios.Oflag &^= unix.ONLCR
	return tcset(f.Fd(), &termios)
}

func cfmakeraw(t unix.Termios) unix.Termios {
	t.Iflag = t.Iflag & ^uint32((unix.IGNBRK | unix.BRKINT | unix.PARMRK | unix.ISTRIP | unix.INLCR | unix.IGNCR | unix.ICRNL | unix.IXON))
	t.Oflag = t.Oflag & ^uint32(unix.OPOST)
	t.Lflag = t.Lflag & ^(uint32(unix.ECHO | unix.ECHONL | unix.ICANON | unix.ISIG | unix.IEXTEN))
	t.Cflag = t.Cflag & ^(uint32(unix.CSIZE | unix.PARENB))
	t.Cflag = t.Cflag | unix.CS8
	t.Cc[unix.VMIN] = 1
	t.Cc[unix.VTIME] = 0

	return t
}
