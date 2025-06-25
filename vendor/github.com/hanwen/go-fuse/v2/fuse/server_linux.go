// Copyright 2016 the Go-FUSE Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package fuse

import (
	"syscall"
)

const useSingleReader = false

func (ms *Server) write(req *request) Status {
	if req.outPayloadSize() == 0 {
		err := handleEINTR(func() error {
			_, err := syscall.Write(ms.mountFd, req.outputBuf)
			return err
		})
		return ToStatus(err)
	}
	if req.fdData != nil {
		if ms.canSplice {
			err := ms.trySplice(req, req.fdData)
			if err == nil {
				req.readResult.Done()
				return OK
			}
			ms.opts.Logger.Println("trySplice:", err)
		}

		req.outPayload, req.status = req.fdData.Bytes(req.outPayload)
		req.serializeHeader(len(req.outPayload))
	}

	_, err := writev(ms.mountFd, [][]byte{req.outputBuf, req.outPayload})
	if req.readResult != nil {
		req.readResult.Done()
	}
	return ToStatus(err)
}
