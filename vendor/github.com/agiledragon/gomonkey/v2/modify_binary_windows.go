package gomonkey

import (
    "syscall"
    "unsafe"
)

func modifyBinary(target uintptr, bytes []byte) {
    function := entryAddress(target, len(bytes))
    
    proc := syscall.NewLazyDLL("kernel32.dll").NewProc("VirtualProtect")
    const PROT_READ_WRITE = 0x40
    var old uint32
    result, _, _ := proc.Call(target, uintptr(len(bytes)), uintptr(PROT_READ_WRITE), uintptr(unsafe.Pointer(&old)))
    if result == 0 {
        panic(result)
    }
    copy(function, bytes)
    
    var ignore uint32
    result, _, _ = proc.Call(target, uintptr(len(bytes)), uintptr(old), uintptr(unsafe.Pointer(&ignore)))
    if result == 0 {
        panic(result)
    }
}