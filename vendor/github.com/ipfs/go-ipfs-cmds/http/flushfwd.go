package http

import (
	"github.com/ipfs/go-ipfs-cmds"
	"net/http"
)

type flushfwder struct {
	cmds.ResponseEmitter
	http.Flusher
}

func NewFlushForwarder(r cmds.ResponseEmitter, f http.Flusher) ResponseEmitter {
	return flushfwder{ResponseEmitter: r, Flusher: f}
}
