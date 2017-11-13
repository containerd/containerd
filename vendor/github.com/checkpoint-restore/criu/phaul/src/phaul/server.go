package phaul

import (
	"fmt"

	"github.com/golang/protobuf/proto"
	"github.com/checkpoint-restore/criu/lib/go/src/criu"
	"github.com/checkpoint-restore/criu/lib/go/src/rpc"
	"path/filepath"
)

type PhaulServer struct {
	cfg  PhaulConfig
	imgs *images
	cr   *criu.Criu
}

/*
 * Main entry point. Make the server with comm and call PhaulRemote
 * methods on it upon client requests.
 */
func MakePhaulServer(c PhaulConfig) (*PhaulServer, error) {
	img, err := preparePhaulImages(c.Wdir)
	if err != nil {
		return nil, err
	}

	cr := criu.MakeCriu()

	return &PhaulServer{imgs: img, cfg: c, cr: cr}, nil
}

/*
 * PhaulRemote methods
 */
func (s *PhaulServer) StartIter() error {
	fmt.Printf("S: start iter\n")
	psi := rpc.CriuPageServerInfo{
		Address: proto.String(s.cfg.Addr),
		Port: proto.Int32(int32(s.cfg.Port)),
	}
	opts := rpc.CriuOpts{
		LogLevel: proto.Int32(4),
		LogFile:  proto.String("ps.log"),
		Ps:       &psi,
	}

	prev_p := s.imgs.lastImagesDir()
	img_dir, err := s.imgs.openNextDir()
	if err != nil {
		return err
	}
	defer img_dir.Close()

	opts.ImagesDirFd = proto.Int32(int32(img_dir.Fd()))
	if prev_p != "" {
		rel, err := filepath.Rel(img_dir.Name(), prev_p)
		if err != nil {
			return err
		}
		opts.ParentImg = proto.String(rel)
	}

	return s.cr.StartPageServer(opts)
}

func (s *PhaulServer) StopIter() error {
	return nil
}

/*
 * Server-local methods
 */
func (s *PhaulServer) LastImagesDir() string {
	return s.imgs.lastImagesDir()
}

func (s *PhaulServer) GetCriu() *criu.Criu {
	return s.cr
}
