package cgroups

/*
#include <unistd.h>
*/
import "C"

func getClockTicks() uint64 {
	return uint64(C.sysconf(C._SC_CLK_TCK))
}
