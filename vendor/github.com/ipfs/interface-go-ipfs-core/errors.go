package iface

import "errors"

var (
	ErrIsDir        = errors.New("this dag node is a directory")
	ErrNotFile      = errors.New("this dag node is not a regular file")
	ErrOffline      = errors.New("this action must be run in online mode, try running 'ipfs daemon' first")
	ErrNotSupported = errors.New("operation not supported")
)
