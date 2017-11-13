package phaul

import (
	"os"

	"github.com/golang/protobuf/proto"
	"github.com/checkpoint-restore/criu/phaul/src/stats"
)

/* FIXME: report stats from CriuResp */
func criuGetDumpStats(img_dir *os.File) (*stats.DumpStatsEntry, error) {
	stf, err := os.Open(img_dir.Name() + "/stats-dump")
	if err != nil {
		return nil, err
	}
	defer stf.Close()

	buf := make([]byte, 2*4096)
	sz, err := stf.Read(buf)
	if err != nil {
		return nil, err
	}

	st := &stats.StatsEntry{}
	// Skip 2 magic values and entry size
	err = proto.Unmarshal(buf[12:sz], st)
	if err != nil {
		return nil, err
	}

	return st.GetDump(), nil
}
