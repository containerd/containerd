package server

// cgroupDelegateAnnotations returns systemd Delegate annotations for writable cgroup
// containers on cgroup v2 when the workload is not privileged.
func cgroupDelegateAnnotations(cgroupWritable, unifiedCgroups, privileged bool) map[string]string {
	if !cgroupWritable || !unifiedCgroups || privileged {
		return nil
	}
	return map[string]string{
		"org.systemd.property.Delegate": "true",
	}
}
