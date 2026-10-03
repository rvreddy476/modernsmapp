//go:build windows

package main

import (
	"os"
	"syscall"
	"unsafe"
)

// readNoEcho clears ENABLE_ECHO_INPUT on the console for the read. When the
// handle is not a console (piped input) it reads plainly, which is what a
// scripted run wants.
func readNoEcho(f *os.File) (string, error) {
	k32 := syscall.NewLazyDLL("kernel32.dll")
	getMode := k32.NewProc("GetConsoleMode")
	setMode := k32.NewProc("SetConsoleMode")
	h := f.Fd()
	var mode uint32
	if r, _, _ := getMode.Call(h, uintptr(unsafe.Pointer(&mode))); r == 0 {
		return readLine(f)
	}
	const enableEchoInput = 0x0004
	setMode.Call(h, uintptr(mode&^enableEchoInput))
	defer setMode.Call(h, uintptr(mode))
	return readLine(f)
}
