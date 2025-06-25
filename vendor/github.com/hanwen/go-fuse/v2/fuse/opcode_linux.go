// Copyright 2024 the Go-FUSE Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package fuse

import "unsafe"

func doStatx(server *protocolServer, req *request) {
	in := (*StatxIn)(req.inData())
	out := (*StatxOut)(req.outData())

	req.status = server.fileSystem.Statx(req.cancel, in, out)
}

func init() {
	operationHandlers[_OP_STATX] = &operationHandler{
		Name:       "STATX",
		Func:       doStatx,
		InType:     StatxIn{},
		OutType:    StatxOut{},
		InputSize:  unsafe.Sizeof(StatxIn{}),
		OutputSize: unsafe.Sizeof(StatxOut{}),
	}
	checkFixedBufferSize()
}
