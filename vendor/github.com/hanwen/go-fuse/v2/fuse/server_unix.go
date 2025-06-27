//go:build !linux

package fuse

import (
	"golang.org/x/sys/unix"
)

// OSX and FreeBSD has races when multiple routines read
// from the FUSE device: on unmount, sometime some reads
// do not error-out, meaning that unmount will hang.
const useSingleReader = true

func (ms *Server) write(req *request) Status {
	if req.outPayloadSize() == 0 {
		err := handleEINTR(func() error {
			_, err := unix.Write(ms.mountFd, req.outputBuf)
			return err
		})
		return ToStatus(err)
	}

	if req.fdData != nil {
		req.outPayload, req.status = req.fdData.Bytes(req.outPayload)
		req.serializeHeader(len(req.outPayload))
	}

	_, err := writev(int(ms.mountFd), [][]byte{req.outputBuf, req.outPayload})
	if req.readResult != nil {
		req.readResult.Done()
	}
	return ToStatus(err)
}
