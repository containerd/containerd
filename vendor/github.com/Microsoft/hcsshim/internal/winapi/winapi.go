package winapi

//go:generate go run ..\..\mksyscall_windows.go -output zsyscall_windows.go bindflt.go user.go console.go system.go net.go path.go thread.go jobobject.go logon.go memory.go process.go processor.go devices.go filesystem.go errors.go
