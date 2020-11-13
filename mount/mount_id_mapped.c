/* SPDX-License-Identifier: LGPL-2.1+ */

#define _GNU_SOURCE

#include <fcntl.h>
#include <limits.h>
#include <stdint.h>
#include <stdio.h>
#include <sys/syscall.h>
#include <linux/types.h>
#include <unistd.h>

#ifndef MOUNT_ATTR_IDMAP
#define MOUNT_ATTR_IDMAP 0x00100000
#endif

#ifndef AT_RECURSIVE
#define AT_RECURSIVE 0x8000
#endif

#ifndef __NR_mount_setattr
	#if defined __alpha__
		#define __NR_mount_setattr 551
	#elif defined _MIPS_SIM
		#if _MIPS_SIM == _MIPS_SIM_ABI32	/* o32 */
			#define __NR_mount_setattr 4441
		#endif
		#if _MIPS_SIM == _MIPS_SIM_NABI32	/* n32 */
			#define __NR_mount_setattr 6441
		#endif
		#if _MIPS_SIM == _MIPS_SIM_ABI64	/* n64 */
			#define __NR_mount_setattr 5441
		#endif
	#elif defined __ia64__
		#define __NR_mount_setattr (441 + 1024)
	#else
		#define __NR_mount_setattr 441
	#endif
#endif

struct mount_attr {
	__u64 attr_set;
	__u64 attr_clr;
	__u64 propagation;
	__u32 userns;
	__u32 reserved[0];
};

static inline int sys_mount_setattr(int dfd, const char *path, unsigned int flags,
				    struct mount_attr *attr, size_t size)
{
	return syscall(__NR_mount_setattr, dfd, path, flags, attr, size);
}

int mount_id_mapped(const char *target, pid_t pid) {
	int target_fd;
	int userns_fd;
	int ret;
	struct mount_attr attr = {};
	char path[PATH_MAX] = {};

	snprintf(path, sizeof(path), "/proc/%d/ns/user", pid);
	userns_fd = open(path, O_RDONLY | O_CLOEXEC);
	if (userns_fd < 0) {
		return userns_fd;
	}

	attr.userns = userns_fd;
	attr.attr_set = MOUNT_ATTR_IDMAP;

	target_fd = open(target, O_RDONLY | O_CLOEXEC);
	if (target_fd < 0) {
		return target_fd;
	}

	ret = sys_mount_setattr(target_fd, "", AT_EMPTY_PATH | AT_RECURSIVE, &attr, sizeof(attr));
	if (ret < 0) {
		return ret;
	}
	close(target_fd);

	return 0;
}
