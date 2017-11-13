package phaul

import (
	"fmt"

	"github.com/golang/protobuf/proto"
	"github.com/checkpoint-restore/criu/lib/go/src/criu"
	"github.com/checkpoint-restore/criu/lib/go/src/rpc"
	"github.com/checkpoint-restore/criu/phaul/src/stats"
)

const minPagesWritten uint64 = 64
const maxIters int = 8
const maxGrowDelta int64 = 32

type PhaulClient struct {
	local  PhaulLocal
	remote PhaulRemote
	cfg    PhaulConfig
}

/*
 * Main entry point. Caller should create the client object by
 * passing here local, remote and comm. See comment in corresponding
 * interfaces/structs for explanation.
 *
 * Then call client.Migrate() and enjoy :)
 */
func MakePhaulClient(l PhaulLocal, r PhaulRemote, c PhaulConfig) (*PhaulClient, error) {
	return &PhaulClient{local: l, remote: r, cfg: c}, nil
}

func isLastIter(iter int, stats *stats.DumpStatsEntry, prev_stats *stats.DumpStatsEntry) bool {
	if iter >= maxIters {
		fmt.Printf("`- max iters reached\n")
		return true
	}

	pagesWritten := stats.GetPagesWritten()
	if pagesWritten < minPagesWritten {
		fmt.Printf("`- tiny pre-dump (%d) reached\n", int(pagesWritten))
		return true
	}

	pages_delta := int64(pagesWritten) - int64(prev_stats.GetPagesWritten())
	if pages_delta >= maxGrowDelta {
		fmt.Printf("`- grow iter (%d) reached\n", int(pages_delta))
		return true
	}

	return false
}

func (pc *PhaulClient) Migrate() error {
	criu := criu.MakeCriu()
	psi := rpc.CriuPageServerInfo{
		Address: proto.String(pc.cfg.Addr),
		Port: proto.Int32(int32(pc.cfg.Port)),
	}
	opts := rpc.CriuOpts{
		Pid:      proto.Int32(int32(pc.cfg.Pid)),
		LogLevel: proto.Int32(4),
		LogFile:  proto.String("pre-dump.log"),
		Ps:       &psi,
	}

	err := criu.Prepare()
	if err != nil {
		return err
	}

	defer criu.Cleanup()

	imgs, err := preparePhaulImages(pc.cfg.Wdir)
	if err != nil {
		return err
	}
	prev_stats := &stats.DumpStatsEntry{}
	iter := 0

	for {
		err = pc.remote.StartIter()
		if err != nil {
			return err
		}

		prev_p := imgs.lastImagesDir()
		img_dir, err := imgs.openNextDir()
		if err != nil {
			return err
		}

		opts.ImagesDirFd = proto.Int32(int32(img_dir.Fd()))
		if prev_p != "" {
			opts.ParentImg = proto.String(prev_p)
		}

		err = criu.PreDump(opts, nil)
		img_dir.Close()
		if err != nil {
			return err
		}

		err = pc.remote.StopIter()
		if err != nil {
			return err
		}

		st, err := criuGetDumpStats(img_dir)
		if err != nil {
			return err
		}

		if isLastIter(iter, st, prev_stats) {
			break
		}

		prev_stats = st
	}

	err = pc.remote.StartIter()
	if err == nil {
		prev_p := imgs.lastImagesDir()
		err = pc.local.DumpCopyRestore(criu, pc.cfg, prev_p)
		err2 := pc.remote.StopIter()
		if err == nil {
			err = err2
		}
	}

	return err
}
