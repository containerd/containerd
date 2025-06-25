package fuse

import "fmt"

func (s *Server) setSplice() {
	s.canSplice = false
}

func (ms *Server) trySplice(header []byte, req *request, fdData *readResultFd) error {
	return fmt.Errorf("unimplemented")
}
