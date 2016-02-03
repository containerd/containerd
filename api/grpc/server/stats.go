package server

import (
	"github.com/docker/containerd/api/grpc/types"
	"github.com/docker/containerd/runtime"
	"github.com/docker/containerd/supervisor"
	"github.com/opencontainers/runc/libcontainer"
	"github.com/opencontainers/runc/libcontainer/cgroups"
	"golang.org/x/net/context"
)

func (s *apiServer) Stats(ctx context.Context, r *types.StatsRequest) (*types.StatsResponse, error) {
	res := &types.StatsResponse{
		Stats: make(map[string]*types.Stats),
	}
	for _, id := range r.Ids {
		e := supervisor.NewEvent(supervisor.StatsEventType)
		e.ID = id
		s.sv.SendEvent(e)
		if err := <-e.Err; err != nil {
			res.Errors = append(res.Errors, &types.StatError{Id: id, Error: err.Error()})
			continue
		}
		res.Stats[id] = convertStatsToPb(e.Stats)
	}
	return res, nil
}

func convertBlkioEntryToPb(b []cgroups.BlkioStatEntry) []*types.BlkioStatsEntry {
	var pbEs []*types.BlkioStatsEntry
	for _, e := range b {
		pbEs = append(pbEs, &types.BlkioStatsEntry{
			Major: e.Major,
			Minor: e.Minor,
			Op:    e.Op,
			Value: e.Value,
		})
	}
	return pbEs
}

func convertStatsToPb(st *runtime.Stat) *types.Stats {
	pbSt := &types.Stats{
		Timestamp:   uint64(st.Timestamp.Unix()),
		CgroupStats: &types.CgroupStats{},
	}
	lcSt, ok := st.Data.(*libcontainer.Stats)
	if !ok {
		return pbSt
	}
	cpuSt := lcSt.CgroupStats.CpuStats
	pbSt.CgroupStats.CpuStats = &types.CpuStats{
		CpuUsage: &types.CpuUsage{
			TotalUsage:        cpuSt.CpuUsage.TotalUsage,
			PercpuUsage:       cpuSt.CpuUsage.PercpuUsage,
			UsageInKernelmode: cpuSt.CpuUsage.UsageInKernelmode,
			UsageInUsermode:   cpuSt.CpuUsage.UsageInUsermode,
		},
		ThrottlingData: &types.ThrottlingData{
			Periods:          cpuSt.ThrottlingData.Periods,
			ThrottledPeriods: cpuSt.ThrottlingData.ThrottledPeriods,
			ThrottledTime:    cpuSt.ThrottlingData.ThrottledTime,
		},
	}
	memSt := lcSt.CgroupStats.MemoryStats
	pbSt.CgroupStats.MemoryStats = &types.MemoryStats{
		Cache: memSt.Cache,
		Usage: &types.MemoryData{
			Usage:    memSt.Usage.Usage,
			MaxUsage: memSt.Usage.MaxUsage,
			Failcnt:  memSt.Usage.Failcnt,
		},
		SwapUsage: &types.MemoryData{
			Usage:    memSt.SwapUsage.Usage,
			MaxUsage: memSt.SwapUsage.MaxUsage,
			Failcnt:  memSt.SwapUsage.Failcnt,
		},
	}
	blkSt := lcSt.CgroupStats.BlkioStats
	pbSt.CgroupStats.BlkioStats = &types.BlkioStats{
		IoServiceBytesRecursive: convertBlkioEntryToPb(blkSt.IoServiceBytesRecursive),
		IoServicedRecursive:     convertBlkioEntryToPb(blkSt.IoServicedRecursive),
		IoQueuedRecursive:       convertBlkioEntryToPb(blkSt.IoQueuedRecursive),
		IoServiceTimeRecursive:  convertBlkioEntryToPb(blkSt.IoServiceTimeRecursive),
		IoWaitTimeRecursive:     convertBlkioEntryToPb(blkSt.IoWaitTimeRecursive),
		IoMergedRecursive:       convertBlkioEntryToPb(blkSt.IoMergedRecursive),
		IoTimeRecursive:         convertBlkioEntryToPb(blkSt.IoTimeRecursive),
		SectorsRecursive:        convertBlkioEntryToPb(blkSt.SectorsRecursive),
	}
	pbSt.CgroupStats.HugetlbStats = make(map[string]*types.HugetlbStats)
	for k, st := range lcSt.CgroupStats.HugetlbStats {
		pbSt.CgroupStats.HugetlbStats[k] = &types.HugetlbStats{
			Usage:    st.Usage,
			MaxUsage: st.MaxUsage,
			Failcnt:  st.Failcnt,
		}
	}
	return pbSt
}
