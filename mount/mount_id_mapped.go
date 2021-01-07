package mount

import (
	"fmt"
	"os/exec"
	"syscall"
	"unsafe"
)

/*
#include <sys/types.h>
#include <stdlib.h>
#cgo CFLAGS: -Wall
int mount_id_mapped(const char *target, pid_t pid);
 */
import "C"

func parse_map(mapstr string) ([]syscall.SysProcIDMap) {
	var (
		container int
		host int
		size int
	)

	// TODO: Support multiple mappings
	fmt.Sscanf(mapstr, "%d:%d:%d", &container, &host, &size)

	return []syscall.SysProcIDMap{
		{
			ContainerID: container,
			HostID:      host,
			Size:        size,
		},
	}
}

func map_mount(uidmap string, gidmap string, target string) error {
	// TODO: Avoid dependency on /bin/sh or do in a complete different way
	cmd := exec.Command("/bin/sh")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags: syscall.CLONE_NEWUSER,
		UidMappings: parse_map(uidmap),
		GidMappings: parse_map(gidmap),
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("Error running the /bin/sh command - %s\n", err)
	}

	// TODO: Implement the whole thing in Go
	dst := C.CString(target)
	ret := C.mount_id_mapped(dst, C.int(cmd.Process.Pid))
	C.free(unsafe.Pointer(dst))

	if ret != C.int(0) {
		return fmt.Errorf("error executing mount_id_mapped")
	}

	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("Error running the /bin/sh command - %s\n", err)
	}

	return nil
}
